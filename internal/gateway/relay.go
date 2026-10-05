package gateway

import (
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

type channelOutput struct {
	channel     ssh.Channel
	stderr      io.Writer
	mu          sync.Mutex
	writeClosed bool
	closed      bool
}

func newChannelOutput(channel ssh.Channel) *channelOutput {
	return &channelOutput{channel: channel, stderr: channel.Stderr()}
}

func (o *channelOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.writeClosed || o.closed {
		return 0, io.ErrClosedPipe
	}
	return o.channel.Write(data)
}

func (o *channelOutput) StderrWriter() io.Writer {
	return channelStderrWriter{output: o}
}

func (o *channelOutput) writeStderr(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.writeClosed || o.closed {
		return 0, io.ErrClosedPipe
	}
	return o.stderr.Write(data)
}

func (o *channelOutput) CloseWrite() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.writeClosed || o.closed {
		return nil
	}
	if err := o.channel.CloseWrite(); err != nil {
		return err
	}
	o.writeClosed = true
	return nil
}

func (o *channelOutput) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	o.writeClosed = true
	o.closed = true
	return o.channel.Close()
}

type channelStderrWriter struct {
	output *channelOutput
}

func (w channelStderrWriter) Write(data []byte) (int, error) {
	return w.output.writeStderr(data)
}

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
		bridgeChannel(newChannelOutput(accepted), acceptedRequests, newChannelOutput(outgoing), outgoingRequests)
	}
}

func bridgeChannel(
	left *channelOutput,
	leftRequests <-chan *ssh.Request,
	right *channelOutput,
	rightRequests <-chan *ssh.Request,
) <-chan struct{} {
	guestDone := make(chan struct{})
	go bridgeChannelRequests(leftRequests, right.channel)
	go bridgeChannelRequests(rightRequests, left.channel)
	go copyAndCloseWrite(right, left.channel)
	go func() {
		copyAndCloseWrite(left, right.channel)
		close(guestDone)
	}()
	return guestDone
}

// bridgeSessionChannel leaves the client's write side open when the guest
// finishes output. The session lifecycle handler owns the final close so it
// can write a TTL-expiry notice before closing the SSH channel.
func bridgeSessionChannel(
	left *channelOutput,
	leftRequests <-chan *ssh.Request,
	right *channelOutput,
	rightRequests <-chan *ssh.Request,
) <-chan struct{} {
	guestDone := make(chan struct{})
	go bridgeChannelRequests(leftRequests, right.channel)
	go bridgeChannelRequests(rightRequests, left.channel)
	go copyAndCloseWrite(right, left.channel)
	go func() {
		_, _ = io.Copy(left, right.channel)
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

func copyAndCloseWrite(destination *channelOutput, source ssh.Channel) {
	_, _ = io.Copy(destination, source)
	_ = destination.CloseWrite()
}
