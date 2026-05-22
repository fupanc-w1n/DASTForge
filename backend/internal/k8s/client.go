package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// Client 包装 clientset + namespace,所有调用走这一处。
type Client struct {
	Clientset *kubernetes.Clientset
	Config    *rest.Config
	Source    string
}

// GetKubeConfig 按 $KUBECONFIG -> k3s -> ~/.kube/config -> /etc/kubernetes/admin.conf 顺序查找。
// 这与 demo/client-go.md 中复用规范一致。
func GetKubeConfig() (*rest.Config, string, string, error) {
	configPaths := []struct {
		path string
		name string
	}{
		{os.Getenv("KUBECONFIG"), "环境变量"},
		{"/etc/rancher/k3s/k3s.yaml", "k3s"},
		{filepath.Join(homedir.HomeDir(), ".kube/config"), "k8s"},
		{"/etc/kubernetes/admin.conf", "k8s"},
	}

	for _, cp := range configPaths {
		if cp.path == "" {
			continue
		}
		if _, err := os.Stat(cp.path); err == nil {
			if config, err := clientcmd.BuildConfigFromFlags("", cp.path); err == nil {
				return config, cp.path, cp.name, nil
			}
		}
	}
	return nil, "", "", fmt.Errorf("kubeconfig not found")
}

// New 建立 Client。允许 cluster 不可用时返回 nil + error,API 调用方按需降级。
func New() (*Client, error) {
	cfg, path, source, err := GetKubeConfig()
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	_ = path
	return &Client{Clientset: cs, Config: cfg, Source: source}, nil
}

// EnsureNamespace 确保命名空间存在(已存在则忽略)。
func (c *Client) EnsureNamespace(ctx context.Context, ns string) error {
	if ns == "" {
		return fmt.Errorf("empty namespace")
	}
	_, err := c.Clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	_, err = c.Clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	if err != nil && apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// CreateOrUpdateConfigMap 写入 Data["config.json"] 内容。
// 沿用 demo/client-go.md 方式二:动态生成 JSON 字符串,不依赖临时文件。
func (c *Client) CreateOrUpdateConfigMap(ctx context.Context, namespace, name, configContent string, labels map[string]string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Data: map[string]string{
			"config.json": configContent,
		},
	}
	cmClient := c.Clientset.CoreV1().ConfigMaps(namespace)
	existing, err := cmClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		_, err = cmClient.Create(ctx, cm, metav1.CreateOptions{})
		return err
	}
	existing.Data = cm.Data
	existing.Labels = cm.Labels
	_, err = cmClient.Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// DeploymentSpec 创建/更新 Deployment 所需字段
type DeploymentSpec struct {
	Namespace      string
	Name           string
	Image          string
	Replicas       int32
	ConfigMapName  string
	ConfigHash     string
	Labels         map[string]string
	Annotations    map[string]string
}

// CreateOrUpdateDeployment 按方式二落地:挂载 ConfigMap 到 /app/config,通过 DAST_CONFIG 指向 /app/config/config.json。
// 镜像标签固定 :latest 时,K8s 自动按 Always 策略拉取。
func (c *Client) CreateOrUpdateDeployment(ctx context.Context, spec DeploymentSpec) error {
	replicas := spec.Replicas
	depClient := c.Clientset.AppsV1().Deployments(spec.Namespace)

	podLabels := map[string]string{}
	for k, v := range spec.Labels {
		podLabels[k] = v
	}
	podLabels["app"] = spec.Name

	annotations := map[string]string{}
	for k, v := range spec.Annotations {
		annotations[k] = v
	}
	if spec.ConfigHash != "" {
		annotations["dast/config-hash"] = spec.ConfigHash
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels:    spec.Labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": spec.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "scanner",
							Image: spec.Image,
							Env: []corev1.EnvVar{
								{Name: "DAST_CONFIG", Value: "/app/config/config.json"},
								{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
								}},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "config-volume", MountPath: "/app/config", ReadOnly: true},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "config-volume",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: spec.ConfigMapName},
								},
							},
						},
					},
				},
			},
		},
	}

	existing, err := depClient.Get(ctx, spec.Name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		_, err = depClient.Create(ctx, dep, metav1.CreateOptions{})
		return err
	}
	existing.Spec = dep.Spec
	existing.Labels = dep.Labels
	_, err = depClient.Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// ScaleDeployment 修改 replicas
func (c *Client) ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) error {
	depClient := c.Clientset.AppsV1().Deployments(namespace)
	dep, err := depClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	dep.Spec.Replicas = &replicas
	_, err = depClient.Update(ctx, dep, metav1.UpdateOptions{})
	return err
}

// DeleteDeployment 删除 Deployment(Foreground)
func (c *Client) DeleteDeployment(ctx context.Context, namespace, name string) error {
	depClient := c.Clientset.AppsV1().Deployments(namespace)
	policy := metav1.DeletePropagationForeground
	err := depClient.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &policy})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// DeleteConfigMap 删除 ConfigMap
func (c *Client) DeleteConfigMap(ctx context.Context, namespace, name string) error {
	err := c.Clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// NodeSummary Node 状态摘要
type NodeSummary struct {
	Name            string `json:"name"`
	InternalIP      string `json:"internal_ip"`
	CPUCapacity     string `json:"cpu_capacity"`
	CPUAllocatable  string `json:"cpu_allocatable"`
	MemoryCapacity  string `json:"memory_capacity"`
	MemoryAllocatable string `json:"memory_allocatable"`
	OSImage         string `json:"os_image"`
	KubeletVersion  string `json:"kubelet_version"`
	Ready           bool   `json:"ready"`
}

// ListNodes 列出 Node 及简要信息
func (c *Client) ListNodes(ctx context.Context) ([]NodeSummary, error) {
	nodes, err := c.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]NodeSummary, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		ns := NodeSummary{
			Name:              n.Name,
			CPUCapacity:       n.Status.Capacity.Cpu().String(),
			CPUAllocatable:    n.Status.Allocatable.Cpu().String(),
			MemoryCapacity:    n.Status.Capacity.Memory().String(),
			MemoryAllocatable: n.Status.Allocatable.Memory().String(),
			OSImage:           n.Status.NodeInfo.OSImage,
			KubeletVersion:    n.Status.NodeInfo.KubeletVersion,
		}
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeInternalIP {
				ns.InternalIP = a.Address
			}
		}
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				ns.Ready = true
			}
		}
		out = append(out, ns)
	}
	return out, nil
}

// PodSummary Pod 状态摘要
type PodSummary struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	PodIP        string `json:"pod_ip"`
	NodeName     string `json:"node_name"`
	Phase        string `json:"phase"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restart_count"`
	PolicyID     string `json:"policy_id,omitempty"`
	Module       string `json:"module,omitempty"`
}

// ListPolicyPods 列出符合 dast/managed-by=dast-scheduler 的 Pod
func (c *Client) ListPolicyPods(ctx context.Context, namespace string) ([]PodSummary, error) {
	pods, err := c.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=dast-scanner",
	})
	if err != nil {
		return nil, err
	}
	out := make([]PodSummary, 0, len(pods.Items))
	for _, p := range pods.Items {
		ps := PodSummary{
			Namespace: p.Namespace,
			Name:      p.Name,
			PodIP:     p.Status.PodIP,
			NodeName:  p.Spec.NodeName,
			Phase:     string(p.Status.Phase),
		}
		ps.PolicyID = p.Labels["dast/policy-id"]
		ps.Module = p.Labels["dast/module"]
		for _, cs := range p.Status.ContainerStatuses {
			ps.RestartCount += cs.RestartCount
			ps.Ready = ps.Ready || cs.Ready
		}
		out = append(out, ps)
	}
	return out, nil
}

// DeploymentStatus 模块 Deployment 状态(策略运行视图)
type DeploymentStatus struct {
	Name      string `json:"name"`
	Module    string `json:"module"`
	Desired   int32  `json:"desired"`
	Ready     int32  `json:"ready"`
	Available int32  `json:"available"`
}

// ListPolicyDeployments 按策略 ID 标签拉取该策略的所有 Deployment
func (c *Client) ListPolicyDeployments(ctx context.Context, namespace, policyID string) ([]DeploymentStatus, error) {
	deps, err := c.Clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("dast/policy-id=%s", policyID),
	})
	if err != nil {
		return nil, err
	}
	out := make([]DeploymentStatus, 0, len(deps.Items))
	for _, d := range deps.Items {
		ds := DeploymentStatus{
			Name:      d.Name,
			Module:    d.Labels["dast/module"],
			Available: d.Status.AvailableReplicas,
			Ready:     d.Status.ReadyReplicas,
		}
		if d.Spec.Replicas != nil {
			ds.Desired = *d.Spec.Replicas
		}
		out = append(out, ds)
	}
	return out, nil
}
