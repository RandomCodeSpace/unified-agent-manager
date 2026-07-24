package session

import (
	"errors"
	"fmt"
	"net"
	"sync"
)

var errLegacyAttachBusy = errors.New("legacy attach already controlled")

type terminalSize struct {
	cols int
	rows int
}

func (size terminalSize) valid() bool {
	return validSize(size.cols, size.rows)
}

type serverMessage struct {
	kind    byte
	payload []byte
}

type attachClient struct {
	conn          net.Conn
	out           chan serverMessage
	done          chan struct{}
	version       protocolVersion
	id            string
	requestedRole clientRole
	assignedRole  clientRole
	order         uint64
	generation    uint64
	latestSize    terminalSize
	hello         clientHello
	ready         bool
	fallback      bool
	once          sync.Once
}

func (client *attachClient) drop() {
	client.once.Do(func() {
		close(client.done)
		if client.conn != nil {
			_ = client.conn.Close()
		}
	})
}

type clientRegistration struct {
	requestedRole clientRole
	hello         clientHello
	size          terminalSize
}

type roleChange struct {
	client     *attachClient
	clientID   string
	role       clientRole
	generation uint64
	reason     string
}

type clientRegistry struct {
	clients    map[*attachClient]struct{}
	controller *attachClient
	standbys   []*attachClient
	nextID     uint64
	nextOrder  uint64
	generation uint64
}

func newClientRegistry() *clientRegistry {
	return &clientRegistry{clients: make(map[*attachClient]struct{})}
}

func (registry *clientRegistry) register(client *attachClient, registration clientRegistration) error {
	if err := validateRequestedRole(registration.requestedRole); err != nil {
		return err
	}
	if client.version == protocolV2 {
		if err := validateClientHello(registration.hello); err != nil {
			return fmt.Errorf("invalid client hello: %w", err)
		}
	} else if registry.controller != nil {
		return errLegacyAttachBusy
	}

	registry.nextID++
	registry.nextOrder++
	client.id = fmt.Sprintf("client-%d", registry.nextID)
	client.order = registry.nextOrder
	client.requestedRole = registration.requestedRole
	client.hello = registration.hello
	if registration.size.valid() {
		client.latestSize = registration.size
	}
	registry.clients[client] = struct{}{}

	if registration.requestedRole == roleObserver {
		client.assignedRole = roleObserver
		client.generation = registry.generation
		return nil
	}
	if registry.controller == nil {
		registry.assignController(client)
		return nil
	}
	client.assignedRole = roleStandby
	client.generation = registry.generation
	registry.standbys = append(registry.standbys, client)
	return nil
}

func (registry *clientRegistry) assignController(client *attachClient) {
	registry.generation++
	client.assignedRole = roleController
	registry.controller = client
	registry.syncGeneration()
}

func (registry *clientRegistry) syncGeneration() {
	for client := range registry.clients {
		client.generation = registry.generation
	}
}

func (registry *clientRegistry) acceptsControl(client *attachClient, generation uint64) bool {
	_, registered := registry.clients[client]
	return registered && registry.controller == client && client.assignedRole == roleController && client.generation == generation
}

func (registry *clientRegistry) updateSize(client *attachClient, generation uint64, size terminalSize) bool {
	switch registry.resizeReason(client, generation, size) {
	case "accepted":
		client.latestSize = size
		return true
	case "not_controller":
		client.latestSize = size
	}
	return false
}

func (registry *clientRegistry) resizeReason(client *attachClient, generation uint64, size terminalSize) string {
	if !size.valid() {
		return "invalid_size"
	}
	if _, registered := registry.clients[client]; !registered {
		return "unknown_client"
	}
	if client.generation != generation {
		return "stale_generation"
	}
	if client.assignedRole == roleObserver {
		return "observer"
	}
	if !registry.acceptsControl(client, generation) {
		return "not_controller"
	}
	return "accepted"
}

func (registry *clientRegistry) transfer(client *attachClient) []roleChange {
	if registry.controller != client || len(registry.standbys) == 0 {
		return nil
	}
	next := registry.standbys[0]
	registry.standbys = registry.standbys[1:]
	client.assignedRole = roleStandby
	registry.standbys = append(registry.standbys, client)
	registry.assignController(next)
	return []roleChange{
		registry.roleChange(client, "transferred"),
		registry.roleChange(next, "transferred"),
	}
}

func (registry *clientRegistry) remove(client *attachClient) []roleChange {
	if _, registered := registry.clients[client]; !registered {
		return nil
	}
	delete(registry.clients, client)
	registry.removeStandby(client)
	if registry.controller != client {
		return nil
	}
	registry.controller = nil
	if len(registry.standbys) == 0 {
		registry.generation++
		registry.syncGeneration()
		return nil
	}
	next := registry.standbys[0]
	registry.standbys = registry.standbys[1:]
	registry.assignController(next)
	return []roleChange{registry.roleChange(next, "promoted")}
}

func (registry *clientRegistry) roleChange(client *attachClient, reason string) roleChange {
	return roleChange{
		client: client, clientID: client.id, role: client.assignedRole, generation: client.generation, reason: reason,
	}
}

func (registry *clientRegistry) removeStandby(client *attachClient) {
	for index, standby := range registry.standbys {
		if standby == client {
			registry.standbys = append(registry.standbys[:index], registry.standbys[index+1:]...)
			return
		}
	}
}

func (registry *clientRegistry) readyClients() []*attachClient {
	clients := make([]*attachClient, 0, len(registry.clients))
	for client := range registry.clients {
		if client.ready {
			clients = append(clients, client)
		}
	}
	return clients
}

func (registry *clientRegistry) drain() []*attachClient {
	clients := make([]*attachClient, 0, len(registry.clients))
	for client := range registry.clients {
		clients = append(clients, client)
	}
	registry.clients = make(map[*attachClient]struct{})
	registry.controller = nil
	registry.standbys = nil
	registry.generation++
	return clients
}
