package docker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"time"

	dockerclient "github.com/docker/docker/client"
	kdopv1alpha1 "github.com/minhtri1612/kdop-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func NewClientFromHost(ctx context.Context, k8s k8sclient.Client, host *kdopv1alpha1.DockerHost) (dockerclient.APIClient, error) {
	return newClient(ctx, k8s, host.Namespace, host.Spec.HostURL, host.Spec.TLSSecretName)
}

func NewClient(ctx context.Context, k8s k8sclient.Client, namespace, hostRef string) (dockerclient.APIClient, error) {
	hostURL := "unix:///var/run/docker.sock"
	tlsSecret := ""

	if hostRef != "" {
		host := &kdopv1alpha1.DockerHost{}
		if err := k8s.Get(ctx, k8sclient.ObjectKey{Namespace: namespace, Name: hostRef}, host); err != nil {
			return nil, fmt.Errorf("get dockerhost %q: %w", hostRef, err)
		}
		hostURL = host.Spec.HostURL
		tlsSecret = host.Spec.TLSSecretName
	}

	return newClient(ctx, k8s, namespace, hostURL, tlsSecret)
}

func newClient(ctx context.Context, k8s k8sclient.Client, namespace, hostURL, tlsSecretName string) (dockerclient.APIClient, error) {
	opts := []dockerclient.Opt{
		dockerclient.WithHost(hostURL),
		dockerclient.WithAPIVersionNegotiation(),
	}

	if tlsSecretName != "" {
		secret := &corev1.Secret{}
		if err := k8s.Get(ctx, k8sclient.ObjectKey{Namespace: namespace, Name: tlsSecretName}, secret); err != nil {
			return nil, err
		}
		tlsCfg, err := tlsFromSecret(secret)
		if err != nil {
			return nil, err
		}
		opts = append(opts, dockerclient.WithHTTPClient(&http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
			Timeout:   10 * time.Second,
		}))
	}

	return dockerclient.NewClientWithOpts(opts...)
}

func tlsFromSecret(secret *corev1.Secret) (*tls.Config, error) {
	ca, ok := secret.Data["ca.pem"]
	if !ok {
		return nil, fmt.Errorf("missing ca.pem")
	}
	cert, ok := secret.Data["cert.pem"]
	if !ok {
		return nil, fmt.Errorf("missing cert.pem")
	}
	key, ok := secret.Data["key.pem"]
	if !ok {
		return nil, fmt.Errorf("missing key.pem")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("invalid ca.pem")
	}
	kp, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}
	return &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{kp}}, nil
}
