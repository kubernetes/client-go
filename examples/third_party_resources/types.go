package main

import (
	"encoding/json"

	"k8s.io/client-go/1.5/pkg/api"
	"k8s.io/client-go/1.5/pkg/api/meta"
	"k8s.io/client-go/1.5/pkg/api/unversioned"
)

type ExampleSpec struct {
	Foo string `json:"foo"`
	Bar bool   `json:"bar"`
}

type Example struct {
	unversioned.TypeMeta `json:",inline"`
	Metadata             api.ObjectMeta `json:"metadata"`

	Spec ExampleSpec `json:"spec"`
}

type ExampleList struct {
	unversioned.TypeMeta `json:",inline"`
	Metadata             unversioned.ListMeta `json:"metadata"`

	Items []Example `json:"items"`
}

// Required to satisfy Object interface
func (e *Example) GetObjectKind() unversioned.ObjectKind {
	return &e.TypeMeta
}

// Required to satisfy ObjectMetaAccessor interface
func (e *Example) GetObjectMeta() meta.Object {
	return &e.Metadata
}

// Required to satisfy Object interface
func (el *ExampleList) GetObjectKind() unversioned.ObjectKind {
	return &el.TypeMeta
}

// Required to satisfy ListMetaAccessor interface
func (el *ExampleList) GetListMeta() unversioned.List {
	return &el.Metadata
}

// The code below is used only to work around a known problem with third-party
// resources and ugorji. If/when these issues are resolved, the code below
// should no longer be required.

type ExampleListCopy ExampleList
type ExampleCopy Example

func (e *Example) UnmarshalJSON(data []byte) error {
	tmp := ExampleCopy{}
	err := json.Unmarshal(data, &tmp)
	if err != nil {
		return err
	}
	tmp2 := Example(tmp)
	*e = tmp2
	return nil
}

func (el *ExampleList) UnmarshalJSON(data []byte) error {
	tmp := ExampleListCopy{}
	err := json.Unmarshal(data, &tmp)
	if err != nil {
		return err
	}
	tmp2 := ExampleList(tmp)
	*el = tmp2
	return nil
}
