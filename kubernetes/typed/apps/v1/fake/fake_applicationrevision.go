package fake

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/client-go/kubernetes/typed/apps/v1"
)

// FakeControllerRevisions implements ControllerRevisionInterface
type FakeApplications struct {
	Fake *FakeAppsV1
	ns   string
}

func (a *FakeApplications) Create(ctx context.Context, applicationRevision v1.ApplicationRevision, opts metav1.CreateOptions) (*v1.ApplicationRevision, error) {
	return &v1.ApplicationRevision{}, nil
}
