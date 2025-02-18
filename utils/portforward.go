package utils

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/kubearmor/kubearmor-client/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForwardOpt details for a pod
type PortForwardOpt struct {
	LocalPort   int64
	RemotePort  int64
	MatchLabels map[string]string
	Namespace   string
	PodName     string
}

// InitiatePortForward : Initiate port forwarding
func InitiatePortForward(c *k8s.Client, localPort int64, remotePort int64, matchLabels map[string]string) (PortForwardOpt, error) {
	pf := PortForwardOpt{
		LocalPort:   localPort,
		RemotePort:  remotePort,
		Namespace:   "",
		MatchLabels: matchLabels,
	}

	// handle port forward
	err := pf.handlePortForward(c)
	if err != nil {
		return pf, err
	}
	return pf, nil
}

// handle port forward to allow grpc to connect at localhost:PORT
func (pf *PortForwardOpt) handlePortForward(c *k8s.Client) error {
	if err := pf.getPodName(c); err != nil {
		return err
	}

	// local port
	lp, err := pf.getLocalPort()
	if err != nil {
		return err
	}
	pf.LocalPort = lp

	err = k8sPortForward(c, *pf)
	if err != nil {
		return fmt.Errorf("\ncould not do kubearmor portforward, error=%s", err.Error())
	}
	return nil

}

// k8s port forward
func k8sPortForward(c *k8s.Client, pf PortForwardOpt) error {
	roundTripper, upgrader, err := spdy.RoundTripperFor(c.Config)
	if err != nil {
		return fmt.Errorf("\nunable to create round tripper and upgrader, error=%s", err.Error())
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", pf.Namespace, pf.PodName)
	hostIP := strings.TrimLeft(c.Config.Host, "https:/")
	serverURL := url.URL{Scheme: "https", Path: path, Host: hostIP}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, &serverURL)

	StopChan, readyChan := make(chan struct{}, 1), make(chan struct{}, 1)
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)

	forwarder, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", pf.LocalPort, pf.RemotePort)},
		StopChan, readyChan, out, errOut)
	if err != nil {
		return fmt.Errorf("\nunable to portforward. error=%s", err.Error())
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- forwarder.ForwardPorts()
	}()

	select {
	case err = <-errChan:
		close(errChan)
		forwarder.Close()
		return fmt.Errorf("could not create port forward %s", err)
	case <-readyChan:
		return nil
	}
}

// Get pod name to enable port forward
func (pf *PortForwardOpt) getPodName(c *k8s.Client) error {
	labelSelector := metav1.LabelSelector{
		MatchLabels: pf.MatchLabels,
	}

	podList, err := c.K8sClientset.CoreV1().Pods(pf.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&labelSelector),
	})

	if err != nil {
		return err
	}
	if len(podList.Items) == 0 {
		return errors.New("kubearmor pod not found")
	}
	pf.PodName = podList.Items[0].GetObjectMeta().GetName()
	pf.Namespace = podList.Items[0].GetObjectMeta().GetNamespace()
	return nil
}

// Returns the local port for the port forwarder
func (pf *PortForwardOpt) getLocalPort() (int64, error) {
	port := pf.LocalPort

	for {
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.FormatInt(port, 10))
		if err == nil {
			if err := listener.Close(); err != nil {
				return -1, err
			}
			fmt.Fprintf(os.Stderr, "local port to be used for port forwarding %s: %d \n", pf.PodName, port)
			return port, nil
		}

		n, err := getRandomInt()
		if err != nil {
			return n, err
		}
		port = n + 32768
	}
}

// get random integer
func getRandomInt() (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(32900-32768))
	if err != nil {
		return -1, errors.New("unable to generate random integer for port")
	}
	return n.Int64(), nil
}
