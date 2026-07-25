package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	managedLabel = "app.kubernetes.io/managed-by"
	managedValue = "tempvm"
	sessionLabel = "tempvm.keiretsu.top/session"
	guestPort    = 2222
)

var sandboxResource = schema.GroupVersionResource{
	Group: "agents.x-k8s.io", Version: "v1beta1", Resource: "sandboxes",
}

type KubernetesBackend struct {
	config  Config
	core    kubernetes.Interface
	dynamic dynamic.Interface
	owner   []metav1.OwnerReference
}

type Session struct {
	Name    string
	Address string
	Signer  ssh.Signer
}

func NewKubernetesBackend(config Config, core kubernetes.Interface, dynamicClient dynamic.Interface) *KubernetesBackend {
	var owner []metav1.OwnerReference
	if name, uid := strings.TrimSpace(os.Getenv("POD_NAME")), strings.TrimSpace(os.Getenv("POD_UID")); name != "" && uid != "" {
		owner = []metav1.OwnerReference{{
			APIVersion: "v1",
			Kind:       "Pod",
			Name:       name,
			UID:        types.UID(uid),
		}}
	}
	return &KubernetesBackend{config: config, core: core, dynamic: dynamicClient, owner: owner}
}

func (b *KubernetesBackend) HostKey(ctx context.Context) (ssh.Signer, error) {
	secrets := b.core.CoreV1().Secrets(b.config.Namespace)
	existing, err := secrets.Get(ctx, b.config.HostKeySecret, metav1.GetOptions{})
	if err == nil {
		return ssh.ParsePrivateKey(existing.Data[corev1.SSHAuthPrivateKey])
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	signer, privateKey, _, err := newSSHKey()
	if err != nil {
		return nil, err
	}
	immutable := true
	_, err = secrets.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      b.config.HostKeySecret,
			Namespace: b.config.Namespace,
			Labels: map[string]string{
				managedLabel:                  managedValue,
				"app.kubernetes.io/component": "host-key",
			},
		},
		Immutable: &immutable,
		Type:      corev1.SecretTypeSSHAuth,
		Data:      map[string][]byte{corev1.SSHAuthPrivateKey: privateKey},
	}, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		existing, err = secrets.Get(ctx, b.config.HostKeySecret, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return ssh.ParsePrivateKey(existing.Data[corev1.SSHAuthPrivateKey])
	}
	return signer, err
}

func (b *KubernetesBackend) RemoveOrphans(ctx context.Context) error {
	selector := sessionLabel
	sandboxes, err := b.dynamic.Resource(sandboxResource).Namespace(b.config.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return err
	}
	for _, sandbox := range sandboxes.Items {
		if err := b.dynamic.Resource(sandboxResource).Namespace(b.config.Namespace).Delete(ctx, sandbox.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	services, err := b.core.CoreV1().Services(b.config.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return err
	}
	for _, service := range services.Items {
		if err := b.core.CoreV1().Services(b.config.Namespace).Delete(ctx, service.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	secrets, err := b.core.CoreV1().Secrets(b.config.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return err
	}
	for _, secret := range secrets.Items {
		if err := b.core.CoreV1().Secrets(b.config.Namespace).Delete(ctx, secret.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (b *KubernetesBackend) Create(ctx context.Context, progress io.Writer) (*Session, error) {
	name, err := randomName()
	if err != nil {
		return nil, err
	}
	signer, _, authorizedKey, err := newSSHKey()
	if err != nil {
		return nil, err
	}
	session := &Session{
		Name:    name,
		Address: fmt.Sprintf("%s.%s.svc:%d", name, b.config.Namespace, guestPort),
		Signer:  signer,
	}
	labels := map[string]string{
		managedLabel: managedValue,
		sessionLabel: name,
	}

	immutable := true
	if _, err := b.core.CoreV1().Secrets(b.config.Namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       b.config.Namespace,
			Labels:          labels,
			OwnerReferences: b.owner,
		},
		Immutable: &immutable,
		Type:      corev1.SecretTypeOpaque,
		Data:      map[string][]byte{"authorized_keys": authorizedKey},
	}, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create session key: %w", err)
	}

	if _, err := b.core.CoreV1().Services(b.config.Namespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       b.config.Namespace,
			Labels:          labels,
			OwnerReferences: b.owner,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name: "ssh",
				Port: guestPort,
			}},
		},
	}, metav1.CreateOptions{}); err != nil {
		_ = b.Delete(context.Background(), session)
		return nil, fmt.Errorf("create guest Service: %w", err)
	}

	sandbox := b.sandbox(name, labels)
	if _, err := b.dynamic.Resource(sandboxResource).Namespace(b.config.Namespace).Create(ctx, sandbox, metav1.CreateOptions{}); err != nil {
		_ = b.Delete(context.Background(), session)
		return nil, fmt.Errorf("create Sandbox: %w", err)
	}

	if err := b.waitReady(ctx, name, progress); err != nil {
		_ = b.Delete(context.Background(), session)
		return nil, err
	}
	return session, nil
}

func (b *KubernetesBackend) Delete(ctx context.Context, session *Session) error {
	var errs []error
	if err := b.dynamic.Resource(sandboxResource).Namespace(b.config.Namespace).Delete(ctx, session.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete Sandbox: %w", err))
	}
	if err := b.core.CoreV1().Services(b.config.Namespace).Delete(ctx, session.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete Service: %w", err))
	}
	if err := b.core.CoreV1().Secrets(b.config.Namespace).Delete(ctx, session.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete session key: %w", err))
	}
	return errors.Join(errs...)
}

func (b *KubernetesBackend) Dial(ctx context.Context, session *Session) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, b.config.BootTimeout)
	defer cancel()
	dialer := net.Dialer{Timeout: 3 * time.Second}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := dialer.DialContext(ctx, "tcp", session.Address)
		if err == nil {
			return conn, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (b *KubernetesBackend) waitReady(parent context.Context, name string, progress io.Writer) error {
	ctx, cancel := context.WithTimeout(parent, b.config.BootTimeout)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastMessage string
	for {
		obj, err := b.dynamic.Resource(sandboxResource).Namespace(b.config.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("read Sandbox status: %w", err)
			}
		} else {
			conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
			for _, item := range conditions {
				condition, ok := item.(map[string]any)
				if !ok || condition["type"] != "Ready" {
					continue
				}
				message, _ := condition["message"].(string)
				if message != "" && message != lastMessage {
					_, _ = fmt.Fprintf(progress, "%s\r\n", message)
					lastMessage = message
				}
				switch condition["status"] {
				case "True":
					return nil
				case "False":
					reason, _ := condition["reason"].(string)
					if reason != "" && reason != "DependenciesNotReady" {
						return fmt.Errorf("Sandbox is not ready: %s: %s", reason, message)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for Linux microVM: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (b *KubernetesBackend) sandbox(name string, labels map[string]string) *unstructured.Unstructured {
	owner := make([]any, 0, len(b.owner))
	for _, ref := range b.owner {
		owner = append(owner, map[string]any{
			"apiVersion": ref.APIVersion,
			"kind":       ref.Kind,
			"name":       ref.Name,
			"uid":        string(ref.UID),
		})
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "agents.x-k8s.io/v1beta1",
		"kind":       "Sandbox",
		"metadata": map[string]any{
			"name":            name,
			"namespace":       b.config.Namespace,
			"labels":          stringMap(labels),
			"ownerReferences": owner,
		},
		"spec": map[string]any{
			"operatingMode":  "Running",
			"shutdownPolicy": "Delete",
			"podTemplate": map[string]any{
				"metadata": map[string]any{"labels": stringMap(labels)},
				"spec": map[string]any{
					"automountServiceAccountToken":  false,
					"enableServiceLinks":            false,
					"runtimeClassName":              b.config.RuntimeClass,
					"terminationGracePeriodSeconds": int64(5),
					"containers": []any{map[string]any{
						"name":            "linux",
						"image":           b.config.GuestImage,
						"imagePullPolicy": "IfNotPresent",
						"ports": []any{map[string]any{
							"name":          "ssh",
							"containerPort": int64(guestPort),
							"protocol":      "TCP",
						}},
						"resources": map[string]any{
							"requests": map[string]any{"cpu": "2", "memory": "2Gi"},
							"limits":   map[string]any{"cpu": "4", "memory": "4Gi"},
						},
						"securityContext": map[string]any{
							"runAsUser":  int64(0),
							"runAsGroup": int64(0),
						},
						"volumeMounts": []any{map[string]any{
							"name":      "session-key",
							"mountPath": "/run/tempvm-credentials",
							"readOnly":  true,
						}},
					}},
					"volumes": []any{map[string]any{
						"name": "session-key",
						"secret": map[string]any{
							"secretName":  name,
							"defaultMode": int64(0400),
						},
					}},
				},
			},
		},
	}}
}

func randomName() (string, error) {
	var value [6]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "tempvm-" + hex.EncodeToString(value[:]), nil
}

func newSSHKey() (ssh.Signer, []byte, []byte, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		return nil, nil, nil, err
	}
	privatePEM := pem.EncodeToMemory(block)
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		return nil, nil, nil, err
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		return nil, nil, nil, err
	}
	return signer, privatePEM, ssh.MarshalAuthorizedKey(sshPublic), nil
}

func stringMap(input map[string]string) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
