package helper

import (
	"encoding/json"
	"sync"

	pcv1alpha1 "github.com/cofyc/advanced-statefulset/pkg/apis/apps/v1alpha1"
	pcclientset "github.com/cofyc/advanced-statefulset/pkg/client/clientset/versioned"
	appsv1alpha1 "github.com/cofyc/advanced-statefulset/pkg/client/clientset/versioned/typed/apps/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
	clientsetappsv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
)

// hijackClient is a special Kubernetes client which hijack statefulset API requests.
type hijackClient struct {
	clientset.Interface
	PingCAPInterface pcclientset.Interface
}

var _ clientset.Interface = &hijackClient{}

func (c hijackClient) AppsV1() clientsetappsv1.AppsV1Interface {
	return hijackAppsV1Client{c.Interface.AppsV1(), c.PingCAPInterface.AppsV1alpha1()}
}

// NewHijackClient creates a new hijacked Kubernetes interface.
func NewHijackClient(client clientset.Interface, pcclient pcclientset.Interface) clientset.Interface {
	return &hijackClient{client, pcclient}
}

type hijackAppsV1Client struct {
	clientsetappsv1.AppsV1Interface
	pingcapV1alpha1Client appsv1alpha1.AppsV1alpha1Interface
}

var _ clientsetappsv1.AppsV1Interface = &hijackAppsV1Client{}

func (c hijackAppsV1Client) StatefulSets(namespace string) clientsetappsv1.StatefulSetInterface {
	return &hijackStatefulset{c.pingcapV1alpha1Client.StatefulSets(namespace)}
}

type hijackStatefulset struct {
	appsv1alpha1.StatefulSetInterface
}

var _ clientsetappsv1.StatefulSetInterface = &hijackStatefulset{}

func (s *hijackStatefulset) Create(sts *appsv1.StatefulSet) (*appsv1.StatefulSet, error) {
	pcsts, err := FromBuiltinStatefulSet(sts)
	if err != nil {
		return nil, err
	}
	pcv1alpha1.SetObjectDefaults_StatefulSet(pcsts) // required if defaulting is not enabled in kube-apiserver
	pcsts, err = s.StatefulSetInterface.Create(pcsts)
	if err != nil {
		return nil, err
	}
	return ToBuiltinStatefulSet(pcsts)
}

func (s *hijackStatefulset) Update(sts *appsv1.StatefulSet) (*appsv1.StatefulSet, error) {
	pcsts, err := FromBuiltinStatefulSet(sts)
	if err != nil {
		return nil, err
	}
	pcsts, err = s.StatefulSetInterface.Update(pcsts)
	if err != nil {
		return nil, err
	}
	return ToBuiltinStatefulSet(pcsts)
}

func (s *hijackStatefulset) UpdateStatus(sts *appsv1.StatefulSet) (*appsv1.StatefulSet, error) {
	pcsts, err := FromBuiltinStatefulSet(sts)
	if err != nil {
		return nil, err
	}
	pcsts, err = s.StatefulSetInterface.UpdateStatus(pcsts)
	if err != nil {
		return nil, err
	}
	return ToBuiltinStatefulSet(pcsts)
}

func (s *hijackStatefulset) Get(name string, options metav1.GetOptions) (*appsv1.StatefulSet, error) {
	pcsts, err := s.StatefulSetInterface.Get(name, options)
	if err != nil {
		return nil, err
	}
	return ToBuiltinStatefulSet(pcsts)
}

func (s *hijackStatefulset) List(opts metav1.ListOptions) (*appsv1.StatefulSetList, error) {
	list, err := s.StatefulSetInterface.List(opts)
	if err != nil {
		return nil, err
	}
	return ToBuiltinStetefulsetList(list)
}

func (s *hijackStatefulset) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	watch, err := s.StatefulSetInterface.Watch(opts)
	if err != nil {
		return nil, err
	}
	return newHijackWatch(watch), nil
}

type hijackWatch struct {
	sync.Mutex
	source  watch.Interface
	result  chan watch.Event
	stopped bool
}

func newHijackWatch(source watch.Interface) watch.Interface {
	w := &hijackWatch{
		source: source,
		result: make(chan watch.Event),
	}
	go w.receive()
	return w
}

func (w *hijackWatch) Stop() {
	w.Lock()
	defer w.Unlock()
	if !w.stopped {
		w.stopped = true
		w.source.Stop()
	}
}

func (w *hijackWatch) receive() {
	defer close(w.result)
	defer w.Stop()
	defer utilruntime.HandleCrash()
	for {
		select {
		case event, ok := <-w.source.ResultChan():
			if !ok {
				return
			}
			asts, ok := event.Object.(*pcv1alpha1.StatefulSet)
			if !ok {
				panic("unreachable")
			}
			sts, err := ToBuiltinStatefulSet(asts)
			if err != nil {
				panic(err)
			}
			w.result <- watch.Event{
				Type:   event.Type,
				Object: sts,
			}
		}
	}
}

func (w *hijackWatch) ResultChan() <-chan watch.Event {
	w.Lock()
	defer w.Unlock()
	return w.result
}

func (s *hijackStatefulset) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *appsv1.StatefulSet, err error) {
	pcsts, err := s.StatefulSetInterface.Patch(name, pt, data, subresources...)
	if err != nil {
		return nil, err
	}
	return ToBuiltinStatefulSet(pcsts)
}

func FromBuiltinStatefulSet(sts *appsv1.StatefulSet) (*pcv1alpha1.StatefulSet, error) {
	data, err := json.Marshal(sts)
	if err != nil {
		return nil, err
	}
	newSet := &pcv1alpha1.StatefulSet{}
	err = json.Unmarshal(data, newSet)
	return newSet, err
}

func ToMultiBuiltinStatefulSet(pcstss []*pcv1alpha1.StatefulSet) ([]*appsv1.StatefulSet, error) {
	stss := make([]*appsv1.StatefulSet, 0)
	for _, pcsts := range pcstss {
		sts, err := ToBuiltinStatefulSet(pcsts)
		if err != nil {
			return nil, err
		}
		stss = append(stss, sts)
	}
	return stss, nil
}

func ToBuiltinStatefulSet(sts *pcv1alpha1.StatefulSet) (*appsv1.StatefulSet, error) {
	data, err := json.Marshal(sts)
	if err != nil {
		return nil, err
	}
	newSet := &appsv1.StatefulSet{}
	err = json.Unmarshal(data, newSet)
	return newSet, err
}

func ToBuiltinStetefulsetList(stsList *pcv1alpha1.StatefulSetList) (*appsv1.StatefulSetList, error) {
	data, err := json.Marshal(stsList)
	if err != nil {
		return nil, err
	}
	newSet := &appsv1.StatefulSetList{}
	err = json.Unmarshal(data, newSet)
	return newSet, err
}