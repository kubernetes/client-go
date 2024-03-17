package v1

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

type ApplicationRevisionGetter interface {
	Applications(name, namespace string, cfg *rest.Config) ApplicationRevisionInterface
}

type ApplicationRevision struct { //dummy shit
	Name          string
	Namespace     string
	ImageName     string
	Port          uint
	ServiceName   string
	NodePort      uint
	ContainerPort uint
}

type ApplicationRevisionInterface interface {
	Create(ctx context.Context, applicationRevision ApplicationRevision, opts metav1.CreateOptions) (*ApplicationRevision, error)
}

type app struct {
	client    rest.Interface
	coreCli   *corev1.CoreV1Client
	namespace string
	name      string
}

func (a *app) Applications(name, namespace, imageName string, port uint) ApplicationRevision {
	return ApplicationRevision{
		Name:          name,
		Namespace:     namespace,
		ImageName:     imageName,
		Port:          port,
		ContainerPort: port,
	}
}
func int32Ptr(i int32) *int32 { return &i }

var service = &v1.Service{
	ObjectMeta: metav1.ObjectMeta{
		Name: "demo-service",
	},
	Spec: v1.ServiceSpec{
		Type: v1.ServiceTypeClusterIP,
		Selector: map[string]string{
			"app": "demo",
		},
		Ports: []v1.ServicePort{
			{
				Name:       "http",
				Port:       8080,
				TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: 8080},
			},
		},
	},
}
var deployment = &appsv1.Deployment{
	ObjectMeta: metav1.ObjectMeta{
		Name: "demo-deployment",
	},
	Spec: appsv1.DeploymentSpec{
		Replicas: int32Ptr(2),
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": "demo",
			},
		},
		Template: v1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "demo",
				},
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "web",
						Image: "nginx:1.12",
						Ports: []v1.ContainerPort{
							{
								Name:          "http",
								Protocol:      v1.ProtocolTCP,
								ContainerPort: 80,
							},
						},
					},
				},
			},
		},
	},
}

func (a *app) Create(ctx context.Context, applicationRevision ApplicationRevision, opts metav1.CreateOptions) (*ApplicationRevision, error) {
	resultDeployment := &appsv1.Deployment{}
	deployment.Spec.Template.Spec.Containers[0].Image = applicationRevision.ImageName
	deployment.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort = int32(applicationRevision.Port)
	err := a.client.Post().
		Namespace(a.namespace).
		Resource("deployments").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(deployment).
		Do(ctx).
		Into(resultDeployment)
	if err != nil {
		panic("here1 " + err.Error())
		// return nil, err
	}

	service.Spec.Ports[0].Port = int32(applicationRevision.Port)
	service.Spec.Ports[0].TargetPort = intstr.FromInt(int(applicationRevision.Port))

	//before this, use the above method and show why there is an error.
	svc, err := a.coreCli.Services(a.namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		panic("here2 " + err.Error())
		// return nil, err
	}
	return &ApplicationRevision{
		Name:          a.name,
		ImageName:     resultDeployment.Spec.Template.Spec.Containers[0].Image,
		Port:          uint(resultDeployment.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort),
		ServiceName:   svc.Name,
		NodePort:      uint(svc.Spec.Ports[0].NodePort),
		ContainerPort: uint(resultDeployment.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort),
	}, nil
}
func NewApp(cli *AppsV1Client, coreCli *corev1.CoreV1Client, name, ns string) ApplicationRevisionInterface {
	return &app{
		client:    cli.RESTClient(),
		coreCli:   coreCli,
		namespace: ns,
		name:      name,
	}
}
