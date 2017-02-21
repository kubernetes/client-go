/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package strategicpatch

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/mergepatch"
	forkedjson "k8s.io/apimachinery/third_party/forked/golang/json"
)

// An alternate implementation of JSON Merge Patch
// (https://tools.ietf.org/html/rfc7386) which supports the ability to annotate
// certain fields with metadata that indicates whether the elements of JSON
// lists should be merged or replaced.
//
// For more information, see the PATCH section of docs/devel/api-conventions.md.
//
// Some of the content of this package was borrowed with minor adaptations from
// evanphx/json-patch and openshift/origin.

const (
	directiveMarker  = "$patch"
	deleteDirective  = "delete"
	replaceDirective = "replace"
	mergeDirective   = "merge"

	deleteFromPrimitiveListDirectivePrefix = "$deleteFromPrimitiveList"
)

// JSONMap is a representations of JSON object encoded as map[string]interface{}
// where the children can be either map[string]interface{}, []interface{} or
// primitive type).
// Operating on JSONMap representation is much faster as it doesn't require any
// json marshaling and/or unmarshaling operations.
type JSONMap map[string]interface{}

// The following code is adapted from github.com/openshift/origin/pkg/util/jsonmerge.
// Instead of defining a Delta that holds an original, a patch and a set of preconditions,
// the reconcile method accepts a set of preconditions as an argument.

// CreateTwoWayMergePatch creates a patch that can be passed to StrategicMergePatch from an original
// document and a modified document, which are passed to the method as json encoded content. It will
// return a patch that yields the modified document when applied to the original document, or an error
// if either of the two documents is invalid.
func CreateTwoWayMergePatch(original, modified []byte, dataStruct interface{}, fns ...mergepatch.PreconditionFunc) ([]byte, error) {
	originalMap := map[string]interface{}{}
	if len(original) > 0 {
		if err := json.Unmarshal(original, &originalMap); err != nil {
			return nil, mergepatch.ErrBadJSONDoc
		}
	}

	modifiedMap := map[string]interface{}{}
	if len(modified) > 0 {
		if err := json.Unmarshal(modified, &modifiedMap); err != nil {
			return nil, mergepatch.ErrBadJSONDoc
		}
	}

	patchMap, err := CreateTwoWayMergeMapPatch(originalMap, modifiedMap, dataStruct, fns...)
	if err != nil {
		return nil, err
	}

	return json.Marshal(patchMap)
}

// CreateTwoWayMergeMapPatch creates a patch from an original and modified JSON objects,
// encoded JSONMap.
// The serialized version of the map can then be passed to StrategicMergeMapPatch.
func CreateTwoWayMergeMapPatch(original, modified JSONMap, dataStruct interface{}, fns ...mergepatch.PreconditionFunc) (JSONMap, error) {
	t, err := getTagStructType(dataStruct)
	if err != nil {
		return nil, err
	}

	patchMap, err := diffMaps(original, modified, t, false, false)
	if err != nil {
		return nil, err
	}

	// Apply the preconditions to the patch, and return an error if any of them fail.
	for _, fn := range fns {
		if !fn(patchMap) {
			return nil, mergepatch.NewErrPreconditionFailed(patchMap)
		}
	}

	return patchMap, nil
}

// Returns a (recursive) strategic merge patch that yields modified when applied to original.
func diffMaps(original, modified map[string]interface{}, t reflect.Type, ignoreChangesAndAdditions, ignoreDeletions bool) (map[string]interface{}, error) {
	patch := map[string]interface{}{}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	for key, modifiedValue := range modified {
		originalValue, ok := original[key]
		if !ok {
			// Key was added, so add to patch
			if !ignoreChangesAndAdditions {
				patch[key] = modifiedValue
			}

			continue
		}

		// The patch has a patch directive
		if key == directiveMarker {
			originalString, ok := originalValue.(string)
			if !ok {
				return nil, fmt.Errorf("invalid value for special key: %s", directiveMarker)
			}

			modifiedString, ok := modifiedValue.(string)
			if !ok {
				return nil, fmt.Errorf("invalid value for special key: %s", directiveMarker)
			}

			if modifiedString != originalString {
				patch[directiveMarker] = modifiedValue
			}

			continue
		}

		if reflect.TypeOf(originalValue) != reflect.TypeOf(modifiedValue) {
			// Types have changed, so add to patch
			if !ignoreChangesAndAdditions {
				patch[key] = modifiedValue
			}

			continue
		}

		// Types are the same, so compare values
		switch originalValueTyped := originalValue.(type) {
		case map[string]interface{}:
			modifiedValueTyped := modifiedValue.(map[string]interface{})
			fieldType, fieldPatchStrategy, _, err := forkedjson.LookupPatchMetadata(t, key)
			if err != nil {
				// We couldn't look up metadata for the field
				// If the values are identical, this doesn't matter, no patch is needed
				if reflect.DeepEqual(originalValue, modifiedValue) {
					continue
				}
				// Otherwise, return the error
				return nil, err
			}

			if fieldPatchStrategy == replaceDirective {
				if !ignoreChangesAndAdditions {
					patch[key] = modifiedValue
				}
				continue
			}

			patchValue, err := diffMaps(originalValueTyped, modifiedValueTyped, fieldType, ignoreChangesAndAdditions, ignoreDeletions)
			if err != nil {
				return nil, err
			}

			if len(patchValue) > 0 {
				patch[key] = patchValue
			}

			continue
		case []interface{}:
			modifiedValueTyped := modifiedValue.([]interface{})
			fieldType, fieldPatchStrategy, fieldPatchMergeKey, err := forkedjson.LookupPatchMetadata(t, key)
			if err != nil {
				// We couldn't look up metadata for the field
				// If the values are identical, this doesn't matter, no patch is needed
				if reflect.DeepEqual(originalValue, modifiedValue) {
					continue
				}
				// Otherwise, return the error
				return nil, err
			}

			if fieldPatchStrategy == mergeDirective {
				addList, deletionList, err := diffLists(originalValueTyped, modifiedValueTyped, fieldType.Elem(), fieldPatchMergeKey, ignoreChangesAndAdditions, ignoreDeletions)
				if err != nil {
					return nil, err
				}

				if len(addList) > 0 {
					patch[key] = addList
				}

				// generate a parallel list for deletion
				if len(deletionList) > 0 {
					parallelDeletionListKey := fmt.Sprintf("%s/%s", deleteFromPrimitiveListDirectivePrefix, key)
					patch[parallelDeletionListKey] = deletionList
				}

				continue
			}
		}

		if !ignoreChangesAndAdditions {
			if !reflect.DeepEqual(originalValue, modifiedValue) {
				// Values are different, so add to patch
				patch[key] = modifiedValue
			}
		}
	}

	if !ignoreDeletions {
		// Add nils for deleted values
		for key := range original {
			_, found := modified[key]
			if !found {
				patch[key] = nil
			}
		}
	}

	return patch, nil
}

// Returns a (recursive) strategic merge patch and a parallel deletion list if necessary.
// Only list of primitives with merge strategy will generate a parallel deletion list.
// These two lists should yield modified when applied to original, for lists with merge semantics.
func diffLists(original, modified []interface{}, t reflect.Type, mergeKey string, ignoreChangesAndAdditions, ignoreDeletions bool) ([]interface{}, []interface{}, error) {
	if len(original) == 0 {
		// Both slices are empty - do nothing
		if len(modified) == 0 || ignoreChangesAndAdditions {
			return nil, nil, nil
		}

		// Old slice was empty - add all elements from the new slice
		return modified, nil, nil
	}

	elementType, err := sliceElementType(original, modified)
	if err != nil {
		return nil, nil, err
	}

	switch elementType.Kind() {
	case reflect.Map:
		patchList, err := diffListsOfMaps(original, modified, t, mergeKey, ignoreChangesAndAdditions, ignoreDeletions)
		return patchList, nil, err
	case reflect.Slice:
		// Lists of Lists are not permitted by the api
		return nil, nil, mergepatch.ErrNoListOfLists
	default:
		return diffListsOfScalars(original, modified, ignoreChangesAndAdditions, ignoreDeletions)
	}
}

// diffListsOfScalars returns 2 lists, the first one is addList and the second one is deletionList.
// Argument ignoreChangesAndAdditions controls if calculate addList. true means not calculate.
// Argument ignoreDeletions controls if calculate deletionList. true means not calculate.
func diffListsOfScalars(original, modified []interface{}, ignoreChangesAndAdditions, ignoreDeletions bool) ([]interface{}, []interface{}, error) {
	// Sort the scalars for easier calculating the diff
	originalScalars := sortScalars(original)
	modifiedScalars := sortScalars(modified)

	originalIndex, modifiedIndex := 0, 0
	addList := []interface{}{}
	deletionList := []interface{}{}

	originalInBounds := originalIndex < len(originalScalars)
	modifiedInBounds := modifiedIndex < len(modifiedScalars)
	bothInBounds := originalInBounds && modifiedInBounds
	for originalInBounds || modifiedInBounds {

		// we need to compare the string representation of the scalar,
		// because the scalar is an interface which doesn't support neither < nor <
		// And that's how func sortScalars compare scalars.
		var originalString, modifiedString string
		if originalInBounds {
			originalString = fmt.Sprintf("%v", originalScalars[originalIndex])
		}

		if modifiedInBounds {
			modifiedString = fmt.Sprintf("%v", modifiedScalars[modifiedIndex])
		}

		switch {
		// scalars are identical
		case bothInBounds && originalString == modifiedString:
			originalIndex++
			modifiedIndex++
		// only modified is in bound
		case !originalInBounds:
			fallthrough
		// modified has additional scalar
		case bothInBounds && originalString > modifiedString:
			if !ignoreChangesAndAdditions {
				modifiedValue := modifiedScalars[modifiedIndex]
				addList = append(addList, modifiedValue)
			}
			modifiedIndex++
		// only original is in bound
		case !modifiedInBounds:
			fallthrough
		// original has additional scalar
		case bothInBounds && originalString < modifiedString:
			if !ignoreDeletions {
				originalValue := originalScalars[originalIndex]
				deletionList = append(deletionList, originalValue)
			}
			originalIndex++
		}

		originalInBounds = originalIndex < len(originalScalars)
		modifiedInBounds = modifiedIndex < len(modifiedScalars)
		bothInBounds = originalInBounds && modifiedInBounds
	}

	return addList, deletionList, nil
}

var errNoMergeKeyFmt = "map: %v does not contain declared merge key: %s"
var errBadArgTypeFmt = "expected a %s, but received a %s"

// Returns a (recursive) strategic merge patch that yields modified when applied to original,
// for a pair of lists of maps with merge semantics.
func diffListsOfMaps(original, modified []interface{}, t reflect.Type, mergeKey string, ignoreChangesAndAdditions, ignoreDeletions bool) ([]interface{}, error) {
	patch := make([]interface{}, 0)

	originalSorted, err := sortMergeListsByNameArray(original, t, mergeKey, false)
	if err != nil {
		return nil, err
	}

	modifiedSorted, err := sortMergeListsByNameArray(modified, t, mergeKey, false)
	if err != nil {
		return nil, err
	}

	originalIndex, modifiedIndex := 0, 0

loopB:
	for ; modifiedIndex < len(modifiedSorted); modifiedIndex++ {
		modifiedMap, ok := modifiedSorted[modifiedIndex].(map[string]interface{})
		if !ok {
			t := reflect.TypeOf(modifiedSorted[modifiedIndex])
			return nil, fmt.Errorf(errBadArgTypeFmt, "map[string]interface{}", t.Kind().String())
		}

		modifiedValue, ok := modifiedMap[mergeKey]
		if !ok {
			return nil, fmt.Errorf(errNoMergeKeyFmt, modifiedMap, mergeKey)
		}

		for ; originalIndex < len(originalSorted); originalIndex++ {
			originalMap, ok := originalSorted[originalIndex].(map[string]interface{})
			if !ok {
				t := reflect.TypeOf(originalSorted[originalIndex])
				return nil, fmt.Errorf(errBadArgTypeFmt, "map[string]interface{}", t.Kind().String())
			}

			originalValue, ok := originalMap[mergeKey]
			if !ok {
				return nil, fmt.Errorf(errNoMergeKeyFmt, originalMap, mergeKey)
			}

			// Assume that the merge key values are comparable strings
			originalString := fmt.Sprintf("%v", originalValue)
			modifiedString := fmt.Sprintf("%v", modifiedValue)
			if originalString >= modifiedString {
				if originalString == modifiedString {
					// Merge key values are equal, so recurse
					patchValue, err := diffMaps(originalMap, modifiedMap, t, ignoreChangesAndAdditions, ignoreDeletions)
					if err != nil {
						return nil, err
					}

					originalIndex++
					if len(patchValue) > 0 {
						patchValue[mergeKey] = modifiedValue
						patch = append(patch, patchValue)
					}
				} else if !ignoreChangesAndAdditions {
					// Item was added, so add to patch
					patch = append(patch, modifiedMap)
				}

				continue loopB
			}

			if !ignoreDeletions {
				// Item was deleted, so add delete directive
				patch = append(patch, map[string]interface{}{mergeKey: originalValue, directiveMarker: deleteDirective})
			}
		}

		break
	}

	if !ignoreDeletions {
		// Delete any remaining items found only in original
		for ; originalIndex < len(originalSorted); originalIndex++ {
			originalMap, ok := originalSorted[originalIndex].(map[string]interface{})
			if !ok {
				t := reflect.TypeOf(originalSorted[originalIndex])
				return nil, fmt.Errorf(errBadArgTypeFmt, "map[string]interface{}", t.Kind().String())
			}

			originalValue, ok := originalMap[mergeKey]
			if !ok {
				return nil, fmt.Errorf(errNoMergeKeyFmt, originalMap, mergeKey)
			}

			patch = append(patch, map[string]interface{}{mergeKey: originalValue, directiveMarker: deleteDirective})
		}
	}

	if !ignoreChangesAndAdditions {
		// Add any remaining items found only in modified
		for ; modifiedIndex < len(modifiedSorted); modifiedIndex++ {
			patch = append(patch, modifiedSorted[modifiedIndex])
		}
	}

	return patch, nil
}

// StrategicMergePatch applies a strategic merge patch. The patch and the original document
// must be json encoded content. A patch can be created from an original and a modified document
// by calling CreateStrategicMergePatch.
func StrategicMergePatch(original, patch []byte, dataStruct interface{}) ([]byte, error) {
	if original == nil {
		original = []byte("{}")
	}

	if patch == nil {
		patch = []byte("{}")
	}

	originalMap := map[string]interface{}{}
	err := json.Unmarshal(original, &originalMap)
	if err != nil {
		return nil, mergepatch.ErrBadJSONDoc
	}

	patchMap := map[string]interface{}{}
	err = json.Unmarshal(patch, &patchMap)
	if err != nil {
		return nil, mergepatch.ErrBadJSONDoc
	}

	result, err := StrategicMergeMapPatch(originalMap, patchMap, dataStruct)
	if err != nil {
		return nil, err
	}

	return json.Marshal(result)
}

// StrategicMergePatch applies a strategic merge patch. The original and patch documents
// must be JSONMap. A patch can be created from an original and modified document by
// calling CreateTwoWayMergeMapPatch.
func StrategicMergeMapPatch(original, patch JSONMap, dataStruct interface{}) (JSONMap, error) {
	t, err := getTagStructType(dataStruct)
	if err != nil {
		return nil, err
	}
	return mergeMap(original, patch, t, true, true)
}

func getTagStructType(dataStruct interface{}) (reflect.Type, error) {
	if dataStruct == nil {
		return nil, fmt.Errorf(errBadArgTypeFmt, "struct", "nil")
	}

	t := reflect.TypeOf(dataStruct)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf(errBadArgTypeFmt, "struct", t.Kind().String())
	}

	return t, nil
}

var errBadPatchTypeFmt = "unknown patch type: %s in map: %v"

// Merge fields from a patch map into the original map. Note: This may modify
// both the original map and the patch because getting a deep copy of a map in
// golang is highly non-trivial.
// flag mergeDeleteList controls if using the parallel list to delete or keeping the list.
// If patch contains any null field (e.g. field_1: null) that is not
// present in original, then to propagate it to the end result use
// ignoreUnmatchedNulls == false.
func mergeMap(original, patch map[string]interface{}, t reflect.Type, mergeDeleteList, ignoreUnmatchedNulls bool) (map[string]interface{}, error) {
	if v, ok := patch[directiveMarker]; ok {
		if v == replaceDirective {
			// If the patch contains "$patch: replace", don't merge it, just use the
			// patch directly. Later on, we can add a single level replace that only
			// affects the map that the $patch is in.
			delete(patch, directiveMarker)
			return patch, nil
		}

		if v == deleteDirective {
			// If the patch contains "$patch: delete", don't merge it, just return
			//  an empty map.
			return map[string]interface{}{}, nil
		}

		return nil, fmt.Errorf(errBadPatchTypeFmt, v, patch)
	}

	// nil is an accepted value for original to simplify logic in other places.
	// If original is nil, replace it with an empty map and then apply the patch.
	if original == nil {
		original = map[string]interface{}{}
	}

	// Start merging the patch into the original.
	for k, patchV := range patch {
		// If found a parallel list for deletion and we are going to merge the list,
		// overwrite the key to the original key and set flag isDeleteList
		isDeleteList := false
		foundParallelListPrefix := strings.HasPrefix(k, deleteFromPrimitiveListDirectivePrefix)
		if foundParallelListPrefix {
			if !mergeDeleteList {
				original[k] = patchV
				continue
			}
			substrings := strings.SplitN(k, "/", 2)
			if len(substrings) <= 1 {
				return nil, mergepatch.ErrBadPatchFormatForPrimitiveList
			}
			isDeleteList = true
			k = substrings[1]
		}

		// If the value of this key is null, delete the key if it exists in the
		// original. Otherwise, check if we want to preserve it or skip it.
		// Preserving the null value is useful when we want to send an explicit
		// delete to the API server.
		if patchV == nil {
			if _, ok := original[k]; ok {
				delete(original, k)
			}

			if ignoreUnmatchedNulls {
				continue
			}
		}

		_, ok := original[k]
		if !ok {
			// If it's not in the original document, just take the patch value.
			original[k] = patchV
			continue
		}

		// If the data type is a pointer, resolve the element.
		if t.Kind() == reflect.Ptr {
			t = t.Elem()
		}

		// If they're both maps or lists, recurse into the value.
		originalType := reflect.TypeOf(original[k])
		patchType := reflect.TypeOf(patchV)
		if originalType == patchType {
			// First find the fieldPatchStrategy and fieldPatchMergeKey.
			fieldType, fieldPatchStrategy, fieldPatchMergeKey, err := forkedjson.LookupPatchMetadata(t, k)
			if err != nil {
				return nil, err
			}

			if originalType.Kind() == reflect.Map && fieldPatchStrategy != replaceDirective {
				typedOriginal := original[k].(map[string]interface{})
				typedPatch := patchV.(map[string]interface{})
				var err error
				original[k], err = mergeMap(typedOriginal, typedPatch, fieldType, mergeDeleteList, ignoreUnmatchedNulls)
				if err != nil {
					return nil, err
				}

				continue
			}

			if originalType.Kind() == reflect.Slice && fieldPatchStrategy == mergeDirective {
				elemType := fieldType.Elem()
				typedOriginal := original[k].([]interface{})
				typedPatch := patchV.([]interface{})
				var err error
				original[k], err = mergeSlice(typedOriginal, typedPatch, elemType, fieldPatchMergeKey, mergeDeleteList, isDeleteList, ignoreUnmatchedNulls)
				if err != nil {
					return nil, err
				}

				continue
			}
		}

		// If originalType and patchType are different OR the types are both
		// maps or slices but we're just supposed to replace them, just take
		// the value from patch.
		original[k] = patchV
	}

	return original, nil
}

// Merge two slices together. Note: This may modify both the original slice and
// the patch because getting a deep copy of a slice in golang is highly
// non-trivial.
func mergeSlice(original, patch []interface{}, elemType reflect.Type, mergeKey string, mergeDeleteList, isDeleteList, ignoreUnmatchedNulls bool) ([]interface{}, error) {
	if len(original) == 0 && len(patch) == 0 {
		return original, nil
	}

	// All the values must be of the same type, but not a list.
	t, err := sliceElementType(original, patch)
	if err != nil {
		return nil, err
	}

	// If the elements are not maps, merge the slices of scalars.
	if t.Kind() != reflect.Map {
		if mergeDeleteList && isDeleteList {
			return deleteFromSlice(original, patch), nil
		}
		// Maybe in the future add a "concat" mode that doesn't
		// uniqify.
		both := append(original, patch...)
		return uniqifyScalars(both), nil
	}

	if mergeKey == "" {
		return nil, fmt.Errorf("cannot merge lists without merge key for type %s", elemType.Kind().String())
	}

	// First look for any special $patch elements.
	patchWithoutSpecialElements := []interface{}{}
	replace := false
	for _, v := range patch {
		typedV := v.(map[string]interface{})
		patchType, ok := typedV[directiveMarker]
		if ok {
			if patchType == deleteDirective {
				mergeValue, ok := typedV[mergeKey]
				if ok {
					// delete all matching entries (based on merge key) from a merging list
					for {
						_, originalKey, found, err := findMapInSliceBasedOnKeyValue(original, mergeKey, mergeValue)
						if err != nil {
							return nil, err
						}

						if !found {
							break
						}
						// Delete the element at originalKey.
						original = append(original[:originalKey], original[originalKey+1:]...)
					}
				} else {
					return nil, fmt.Errorf("delete patch type with no merge key defined")
				}
			} else if patchType == replaceDirective {
				replace = true
				// Continue iterating through the array to prune any other $patch elements.
			} else if patchType == mergeDirective {
				return nil, fmt.Errorf("merging lists cannot yet be specified in the patch")
			} else {
				return nil, fmt.Errorf(errBadPatchTypeFmt, patchType, typedV)
			}
		} else {
			patchWithoutSpecialElements = append(patchWithoutSpecialElements, v)
		}
	}

	if replace {
		return patchWithoutSpecialElements, nil
	}

	patch = patchWithoutSpecialElements

	// Merge patch into original.
	for _, v := range patch {
		// Because earlier we confirmed that all the elements are maps.
		typedV := v.(map[string]interface{})
		mergeValue, ok := typedV[mergeKey]
		if !ok {
			return nil, fmt.Errorf(errNoMergeKeyFmt, typedV, mergeKey)
		}

		// If we find a value with this merge key value in original, merge the
		// maps. Otherwise append onto original.
		originalMap, originalKey, found, err := findMapInSliceBasedOnKeyValue(original, mergeKey, mergeValue)
		if err != nil {
			return nil, err
		}

		if found {
			var mergedMaps interface{}
			var err error
			// Merge into original.
			mergedMaps, err = mergeMap(originalMap, typedV, elemType, mergeDeleteList, ignoreUnmatchedNulls)
			if err != nil {
				return nil, err
			}

			original[originalKey] = mergedMaps
		} else {
			original = append(original, v)
		}
	}

	return original, nil
}

// deleteFromSlice uses the parallel list to delete the items in a list of scalars
func deleteFromSlice(current, toDelete []interface{}) []interface{} {
	currentScalars := uniqifyAndSortScalars(current)
	toDeleteScalars := uniqifyAndSortScalars(toDelete)

	currentIndex, toDeleteIndex := 0, 0
	mergedList := []interface{}{}

	for currentIndex < len(currentScalars) && toDeleteIndex < len(toDeleteScalars) {
		originalString := fmt.Sprintf("%v", currentScalars[currentIndex])
		modifiedString := fmt.Sprintf("%v", toDeleteScalars[toDeleteIndex])

		switch {
		// found an item to delete
		case originalString == modifiedString:
			currentIndex++
		// Request to delete an item that was not found in the current list
		case originalString > modifiedString:
			toDeleteIndex++
		// Found an item that was not part of the deletion list, keep it
		case originalString < modifiedString:
			mergedList = append(mergedList, currentScalars[currentIndex])
			currentIndex++
		}
	}
	return append(mergedList, currentScalars[currentIndex:]...)
}

// This method no longer panics if any element of the slice is not a map.
func findMapInSliceBasedOnKeyValue(m []interface{}, key string, value interface{}) (map[string]interface{}, int, bool, error) {
	for k, v := range m {
		typedV, ok := v.(map[string]interface{})
		if !ok {
			return nil, 0, false, fmt.Errorf("value for key %v is not a map.", k)
		}

		valueToMatch, ok := typedV[key]
		if ok && valueToMatch == value {
			return typedV, k, true, nil
		}
	}

	return nil, 0, false, nil
}

// This function takes a JSON map and sorts all the lists that should be merged
// by key. This is needed by tests because in JSON, list order is significant,
// but in Strategic Merge Patch, merge lists do not have significant order.
// Sorting the lists allows for order-insensitive comparison of patched maps.
func sortMergeListsByName(mapJSON []byte, dataStruct interface{}) ([]byte, error) {
	var m map[string]interface{}
	err := json.Unmarshal(mapJSON, &m)
	if err != nil {
		return nil, err
	}

	newM, err := sortMergeListsByNameMap(m, reflect.TypeOf(dataStruct))
	if err != nil {
		return nil, err
	}

	return json.Marshal(newM)
}

// Function sortMergeListsByNameMap recursively sorts the merge lists by its mergeKey in a map.
func sortMergeListsByNameMap(s map[string]interface{}, t reflect.Type) (map[string]interface{}, error) {
	newS := map[string]interface{}{}
	for k, v := range s {
		if strings.HasPrefix(k, deleteFromPrimitiveListDirectivePrefix) {
			typedV, ok := v.([]interface{})
			if !ok {
				return nil, mergepatch.ErrBadPatchFormatForPrimitiveList
			}
			v = uniqifyAndSortScalars(typedV)
		} else if k != directiveMarker {
			fieldType, fieldPatchStrategy, fieldPatchMergeKey, err := forkedjson.LookupPatchMetadata(t, k)
			if err != nil {
				return nil, err
			}

			// If v is a map or a merge slice, recurse.
			if typedV, ok := v.(map[string]interface{}); ok {
				var err error
				v, err = sortMergeListsByNameMap(typedV, fieldType)
				if err != nil {
					return nil, err
				}
			} else if typedV, ok := v.([]interface{}); ok {
				if fieldPatchStrategy == mergeDirective {
					var err error
					v, err = sortMergeListsByNameArray(typedV, fieldType.Elem(), fieldPatchMergeKey, true)
					if err != nil {
						return nil, err
					}
				}
			}
		}

		newS[k] = v
	}

	return newS, nil
}

// Function sortMergeListsByNameMap recursively sorts the merge lists by its mergeKey in an array.
func sortMergeListsByNameArray(s []interface{}, elemType reflect.Type, mergeKey string, recurse bool) ([]interface{}, error) {
	if len(s) == 0 {
		return s, nil
	}

	// We don't support lists of lists yet.
	t, err := sliceElementType(s)
	if err != nil {
		return nil, err
	}

	// If the elements are not maps...
	if t.Kind() != reflect.Map {
		// Sort the elements, because they may have been merged out of order.
		return uniqifyAndSortScalars(s), nil
	}

	// Elements are maps - if one of the keys of the map is a map or a
	// list, we may need to recurse into it.
	newS := []interface{}{}
	for _, elem := range s {
		if recurse {
			typedElem := elem.(map[string]interface{})
			newElem, err := sortMergeListsByNameMap(typedElem, elemType)
			if err != nil {
				return nil, err
			}

			newS = append(newS, newElem)
		} else {
			newS = append(newS, elem)
		}
	}

	// Sort the maps.
	newS = sortMapsBasedOnField(newS, mergeKey)
	return newS, nil
}

func sortMapsBasedOnField(m []interface{}, fieldName string) []interface{} {
	mapM := mapSliceFromSlice(m)
	ss := SortableSliceOfMaps{mapM, fieldName}
	sort.Sort(ss)
	newS := sliceFromMapSlice(ss.s)
	return newS
}

func mapSliceFromSlice(m []interface{}) []map[string]interface{} {
	newM := []map[string]interface{}{}
	for _, v := range m {
		vt := v.(map[string]interface{})
		newM = append(newM, vt)
	}

	return newM
}

func sliceFromMapSlice(s []map[string]interface{}) []interface{} {
	newS := []interface{}{}
	for _, v := range s {
		newS = append(newS, v)
	}

	return newS
}

type SortableSliceOfMaps struct {
	s []map[string]interface{}
	k string // key to sort on
}

func (ss SortableSliceOfMaps) Len() int {
	return len(ss.s)
}

func (ss SortableSliceOfMaps) Less(i, j int) bool {
	iStr := fmt.Sprintf("%v", ss.s[i][ss.k])
	jStr := fmt.Sprintf("%v", ss.s[j][ss.k])
	return sort.StringsAreSorted([]string{iStr, jStr})
}

func (ss SortableSliceOfMaps) Swap(i, j int) {
	tmp := ss.s[i]
	ss.s[i] = ss.s[j]
	ss.s[j] = tmp
}

func uniqifyAndSortScalars(s []interface{}) []interface{} {
	s = uniqifyScalars(s)
	return sortScalars(s)
}

func sortScalars(s []interface{}) []interface{} {
	ss := SortableSliceOfScalars{s}
	sort.Sort(ss)
	return ss.s
}

func uniqifyScalars(s []interface{}) []interface{} {
	// Clever algorithm to uniqify.
	length := len(s) - 1
	for i := 0; i < length; i++ {
		for j := i + 1; j <= length; j++ {
			if s[i] == s[j] {
				s[j] = s[length]
				s = s[0:length]
				length--
				j--
			}
		}
	}

	return s
}

type SortableSliceOfScalars struct {
	s []interface{}
}

func (ss SortableSliceOfScalars) Len() int {
	return len(ss.s)
}

func (ss SortableSliceOfScalars) Less(i, j int) bool {
	iStr := fmt.Sprintf("%v", ss.s[i])
	jStr := fmt.Sprintf("%v", ss.s[j])
	return sort.StringsAreSorted([]string{iStr, jStr})
}

func (ss SortableSliceOfScalars) Swap(i, j int) {
	tmp := ss.s[i]
	ss.s[i] = ss.s[j]
	ss.s[j] = tmp
}

// Returns the type of the elements of N slice(s). If the type is different,
// another slice or undefined, returns an error.
func sliceElementType(slices ...[]interface{}) (reflect.Type, error) {
	var prevType reflect.Type
	for _, s := range slices {
		// Go through elements of all given slices and make sure they are all the same type.
		for _, v := range s {
			currentType := reflect.TypeOf(v)
			if prevType == nil {
				prevType = currentType
				// We don't support lists of lists yet.
				if prevType.Kind() == reflect.Slice {
					return nil, mergepatch.ErrNoListOfLists
				}
			} else {
				if prevType != currentType {
					return nil, fmt.Errorf("list element types are not identical: %v", fmt.Sprint(slices))
				}
				prevType = currentType
			}
		}
	}

	if prevType == nil {
		return nil, fmt.Errorf("no elements in any of the given slices")
	}

	return prevType, nil
}

// MergingMapsHaveConflicts returns true if the left and right JSON interface
// objects overlap with different values in any key. All keys are required to be
// strings. Since patches of the same Type have congruent keys, this is valid
// for multiple patch types. This method supports strategic merge patch semantics.
func MergingMapsHaveConflicts(left, right map[string]interface{}, dataStruct interface{}) (bool, error) {
	t, err := getTagStructType(dataStruct)
	if err != nil {
		return true, err
	}

	return mergingMapFieldsHaveConflicts(left, right, t, "", "")
}

func mergingMapFieldsHaveConflicts(
	left, right interface{},
	fieldType reflect.Type,
	fieldPatchStrategy, fieldPatchMergeKey string,
) (bool, error) {
	switch leftType := left.(type) {
	case map[string]interface{}:
		switch rightType := right.(type) {
		case map[string]interface{}:
			leftMarker, okLeft := leftType[directiveMarker]
			rightMarker, okRight := rightType[directiveMarker]
			// if one or the other has a directive marker,
			// then we need to consider that before looking at the individual keys,
			// since a directive operates on the whole map.
			if okLeft || okRight {
				// if one has a directive marker and the other doesn't,
				// then we have a conflict, since one is deleting or replacing the whole map,
				// and the other is doing things to individual keys.
				if okLeft != okRight {
					return true, nil
				}

				// if they both have markers, but they are not the same directive,
				// then we have a conflict because they're doing different things to the map.
				if leftMarker != rightMarker {
					return true, nil
				}
			}

			if fieldPatchStrategy == replaceDirective {
				return false, nil
			}

			// Check the individual keys.
			return mapsHaveConflicts(leftType, rightType, fieldType)
		default:
			return true, nil
		}
	case []interface{}:
		switch rightType := right.(type) {
		case []interface{}:
			return slicesHaveConflicts(leftType, rightType, fieldType, fieldPatchStrategy, fieldPatchMergeKey)
		default:
			return true, nil
		}
	case string, float64, bool, int, int64, nil:
		return !reflect.DeepEqual(left, right), nil
	default:
		return true, fmt.Errorf("unknown type: %v", reflect.TypeOf(left))
	}
}

func mapsHaveConflicts(typedLeft, typedRight map[string]interface{}, structType reflect.Type) (bool, error) {
	for key, leftValue := range typedLeft {
		if key != directiveMarker {
			if rightValue, ok := typedRight[key]; ok {
				fieldType, fieldPatchStrategy, fieldPatchMergeKey, err := forkedjson.LookupPatchMetadata(structType, key)
				if err != nil {
					return true, err
				}

				if hasConflicts, err := mergingMapFieldsHaveConflicts(leftValue, rightValue,
					fieldType, fieldPatchStrategy, fieldPatchMergeKey); hasConflicts {
					return true, err
				}
			}
		}
	}

	return false, nil
}

func slicesHaveConflicts(
	typedLeft, typedRight []interface{},
	fieldType reflect.Type,
	fieldPatchStrategy, fieldPatchMergeKey string,
) (bool, error) {
	elementType, err := sliceElementType(typedLeft, typedRight)
	if err != nil {
		return true, err
	}

	valueType := fieldType.Elem()
	if fieldPatchStrategy == mergeDirective {
		// Merging lists of scalars have no conflicts by definition
		// So we only need to check further if the elements are maps
		if elementType.Kind() != reflect.Map {
			return false, nil
		}

		// Build a map for each slice and then compare the two maps
		leftMap, err := sliceOfMapsToMapOfMaps(typedLeft, fieldPatchMergeKey)
		if err != nil {
			return true, err
		}

		rightMap, err := sliceOfMapsToMapOfMaps(typedRight, fieldPatchMergeKey)
		if err != nil {
			return true, err
		}

		return mapsOfMapsHaveConflicts(leftMap, rightMap, valueType)
	}

	// Either we don't have type information, or these are non-merging lists
	if len(typedLeft) != len(typedRight) {
		return true, nil
	}

	// Sort scalar slices to prevent ordering issues
	// We have no way to sort non-merging lists of maps
	if elementType.Kind() != reflect.Map {
		typedLeft = uniqifyAndSortScalars(typedLeft)
		typedRight = uniqifyAndSortScalars(typedRight)
	}

	// Compare the slices element by element in order
	// This test will fail if the slices are not sorted
	for i := range typedLeft {
		if hasConflicts, err := mergingMapFieldsHaveConflicts(typedLeft[i], typedRight[i], valueType, "", ""); hasConflicts {
			return true, err
		}
	}

	return false, nil
}

func sliceOfMapsToMapOfMaps(slice []interface{}, mergeKey string) (map[string]interface{}, error) {
	result := make(map[string]interface{}, len(slice))
	for _, value := range slice {
		typedValue, ok := value.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid element type in merging list:%v", slice)
		}

		mergeValue, ok := typedValue[mergeKey]
		if !ok {
			return nil, fmt.Errorf("cannot find merge key `%s` in merging list element:%v", mergeKey, typedValue)
		}

		result[fmt.Sprintf("%s", mergeValue)] = typedValue
	}

	return result, nil
}

func mapsOfMapsHaveConflicts(typedLeft, typedRight map[string]interface{}, structType reflect.Type) (bool, error) {
	for key, leftValue := range typedLeft {
		if rightValue, ok := typedRight[key]; ok {
			if hasConflicts, err := mergingMapFieldsHaveConflicts(leftValue, rightValue, structType, "", ""); hasConflicts {
				return true, err
			}
		}
	}

	return false, nil
}

// CreateThreeWayMergePatch reconciles a modified configuration with an original configuration,
// while preserving any changes or deletions made to the original configuration in the interim,
// and not overridden by the current configuration. All three documents must be passed to the
// method as json encoded content. It will return a strategic merge patch, or an error if any
// of the documents is invalid, or if there are any preconditions that fail against the modified
// configuration, or, if overwrite is false and there are conflicts between the modified and current
// configurations. Conflicts are defined as keys changed differently from original to modified
// than from original to current. In other words, a conflict occurs if modified changes any key
// in a way that is different from how it is changed in current (e.g., deleting it, changing its
// value). We also propagate values fields that do not exist in original but are explicitly
// defined in modified.
func CreateThreeWayMergePatch(original, modified, current []byte, dataStruct interface{}, overwrite bool, fns ...mergepatch.PreconditionFunc) ([]byte, error) {
	originalMap := map[string]interface{}{}
	if len(original) > 0 {
		if err := json.Unmarshal(original, &originalMap); err != nil {
			return nil, mergepatch.ErrBadJSONDoc
		}
	}

	modifiedMap := map[string]interface{}{}
	if len(modified) > 0 {
		if err := json.Unmarshal(modified, &modifiedMap); err != nil {
			return nil, mergepatch.ErrBadJSONDoc
		}
	}

	currentMap := map[string]interface{}{}
	if len(current) > 0 {
		if err := json.Unmarshal(current, &currentMap); err != nil {
			return nil, mergepatch.ErrBadJSONDoc
		}
	}

	t, err := getTagStructType(dataStruct)
	if err != nil {
		return nil, err
	}

	// The patch is the difference from current to modified without deletions, plus deletions
	// from original to modified. To find it, we compute deletions, which are the deletions from
	// original to modified, and delta, which is the difference from current to modified without
	// deletions, and then apply delta to deletions as a patch, which should be strictly additive.
	deltaMap, err := diffMaps(currentMap, modifiedMap, t, false, true)
	if err != nil {
		return nil, err
	}

	deletionsMap, err := diffMaps(originalMap, modifiedMap, t, true, false)
	if err != nil {
		return nil, err
	}

	patchMap, err := mergeMap(deletionsMap, deltaMap, t, false, false)
	if err != nil {
		return nil, err
	}

	// Apply the preconditions to the patch, and return an error if any of them fail.
	for _, fn := range fns {
		if !fn(patchMap) {
			return nil, mergepatch.NewErrPreconditionFailed(patchMap)
		}
	}

	// If overwrite is false, and the patch contains any keys that were changed differently,
	// then return a conflict error.
	if !overwrite {
		changedMap, err := diffMaps(originalMap, currentMap, t, false, false)
		if err != nil {
			return nil, err
		}

		hasConflicts, err := MergingMapsHaveConflicts(patchMap, changedMap, dataStruct)
		if err != nil {
			return nil, err
		}

		if hasConflicts {
			return nil, mergepatch.NewErrConflict(mergepatch.ToYAMLOrError(patchMap), mergepatch.ToYAMLOrError(changedMap))
		}
	}

	return json.Marshal(patchMap)
}
