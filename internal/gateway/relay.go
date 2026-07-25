package gateway

import (
	"io"

	"golang.org/x/crypto/ssh"
)

func bridgeGlobalRequests(requests <-chan *ssh.Request, destination ssh.Conn) {
	for request := range requests {
		ok, response, err := destination.SendRequest(request.Type, request.WantReply, request.Payload)
		if err != nil {
			return
		}
		if request.WantReply {
			_ = request.Reply(ok, response)
		}
	}
}

func bridgeNewChannels(channels <-chan ssh.NewChannel, destination ssh.Conn) {
	for incoming := range channels {
		outgoing, outgoingRequests, err := destination.OpenChannel(incoming.ChannelType(), incoming.ExtraData())
		if err != nil {
			_ = incoming.Reject(ssh.ConnectionFailed, err.Error())
			continue
		}
		accepted, acceptedRequests, err := incoming.Accept()
		if err != nil {
			_ = outgoing.Close()
			continue
		}
		bridgeChannel(accepted, acceptedRequests, outgoing, outgoingRequests)
	}
}

func bridgeChannel(
	left ssh.Channel,
	leftRequests <-chan *ssh.Request,
	right ssh.Channel,
	rightRequests <-chan *ssh.Request,
) <-chan struct{} {
	guestDone := make(chan struct{})
	go bridgeChannelRequests(leftRequests, right)
	go bridgeChannelRequests(rightRequests, left)
	go copyAndCloseWrite(right, left)
	go func() {
		copyAndCloseWrite(left, right)
		close(guestDone)
	}()
	return guestDone
}

func bridgeChannelRequests(requests <-chan *ssh.Request, destination ssh.Channel) {
	for request := range requests {
		ok, err := destination.SendRequest(request.Type, request.WantReply, request.Payload)
		if err != nil {
			return
		}
		if request.WantReply {
			_ = request.Reply(ok, nil)
		}
	}
}

func copyAndCloseWrite(destination ssh.Channel, source ssh.Channel) {
	_, _ = io.Copy(destination, source)
	_ = destination.CloseWrite()
}
