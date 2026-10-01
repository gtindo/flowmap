package engine

import (
	"context"
	"fmt"
	"io"

	"github.com/gtindo/flowmap/internal/protocol"
)

// Connection is an initialized protocol client attached to an engine session
// running in the same process. Every request still crosses the full JSON-RPC
// framing, so in-process clients exercise the same wire contract as editors.
type Connection struct {
	Client *protocol.Client

	served chan error
}

// StartInProcess runs a new engine session over in-memory pipes and
// initializes a client for it.
// Side Effect (Edge): starts engine and client goroutines.
func StartInProcess(ctx context.Context, options Options, clientInfo protocol.PeerInfo) (*Connection, error) {
	clientToEngine, clientWriter := io.Pipe()
	engineReader, engineToClient := io.Pipe()

	served := make(chan error, 1)
	session := New(options)
	go func() {
		// The session outlives request contexts; Close ends it.
		err := session.Serve(context.WithoutCancel(ctx), clientToEngine, engineToClient)
		_ = engineToClient.Close()
		_ = clientToEngine.Close()
		served <- err
	}()

	connection := &Connection{Client: protocol.NewClient(engineReader, clientWriter), served: served}
	if _, err := connection.Client.Initialize(ctx, protocol.InitializeParams{ClientInfo: clientInfo}); err != nil {
		_ = connection.Client.Close()
		<-served
		return nil, fmt.Errorf("initialize in-process engine: %w", err)
	}

	return connection, nil
}

// Close performs the protocol shutdown and exit sequence and waits for the
// engine session to end.
func (connection *Connection) Close(ctx context.Context) error {
	shutdownErr := connection.Client.Shutdown(ctx)
	_ = connection.Client.Close()

	select {
	case err := <-connection.served:
		if shutdownErr != nil {
			return fmt.Errorf("shut down engine: %w", shutdownErr)
		}
		if err != nil {
			return fmt.Errorf("engine session: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
