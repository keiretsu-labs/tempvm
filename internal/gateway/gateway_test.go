package gateway

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestSSHAuthentication(t *testing.T) {
	signer := testSigner(t)
	gateway := New(DefaultConfig(), nil, signer)

	t.Run("accepts linux without a client key", func(t *testing.T) {
		clientErr, serverErr := handshake(t, gateway.sshConfig, "linux")
		if clientErr != nil {
			t.Fatalf("client handshake: %v", clientErr)
		}
		if serverErr != nil {
			t.Fatalf("server handshake: %v", serverErr)
		}
	})

	t.Run("rejects any other user", func(t *testing.T) {
		clientErr, serverErr := handshake(t, gateway.sshConfig, "root")
		if clientErr == nil {
			t.Fatal("client handshake unexpectedly succeeded")
		}
		if serverErr == nil {
			t.Fatal("server handshake unexpectedly succeeded")
		}
	})
}

func handshake(t *testing.T, config *ssh.ServerConfig, user string) (clientErr, serverErr error) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	deadline := time.Now().Add(5 * time.Second)
	serverDone := make(chan error, 1)
	go func() {
		server, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		if err := server.SetDeadline(deadline); err != nil {
			serverDone <- err
			return
		}
		conn, _, _, err := ssh.NewServerConn(server, config)
		if err == nil {
			_ = conn.Close()
		}
		serverDone <- err
	}()

	client, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	conn, _, _, clientErr := ssh.NewClientConn(client, "pipe", &ssh.ClientConfig{
		User:            user,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if clientErr == nil {
		_ = conn.Close()
	}
	serverErr = <-serverDone
	return clientErr, serverErr
}

func testSigner(t *testing.T) ssh.Signer {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}
