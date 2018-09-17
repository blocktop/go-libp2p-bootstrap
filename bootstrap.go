package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	peerState "github.com/blocktop/go-libp2p-bootstrap/state/peers"
	startedState "github.com/blocktop/go-libp2p-bootstrap/state/started"
	glog "github.com/golang/glog"
	host "github.com/libp2p/go-libp2p-host"
	net "github.com/libp2p/go-libp2p-net"
	peerstore "github.com/libp2p/go-libp2p-peerstore"
	ma "github.com/multiformats/go-multiaddr"
)

//Bootstrap configuration
//"HardBootstrap" is the time after we
//dial to peer's in order to prove if we are connected ot the WWW
//instead of waiting for a delta in our addresses.
//This shouldn't be done too often since it can lead to problems
//(https://github.com/libp2p/go-libp2p-swarm/issues/37).
//You can chose something that is higher than one minute.
type Config struct {
	BootstrapPeers    []string
	MinPeers          int
	BootstrapInterval time.Duration
	HardBootstrap     time.Duration
}

type wrappedTicker struct {
	ticker *time.Ticker
	closer chan struct{}
}

type Bootstrap struct {
	MinPeers       int
	PeerCount      int
	bootstrapPeers []*peerstore.PeerInfo
	host           host.Host
	notifiee       *net.NotifyBundle
	bootstrap      wrappedTicker
	hardBootstrap  wrappedTicker
	startedState   *startedState.State
	peerState      *peerState.State
}

func (b *Bootstrap) GetMinPeers() int {
	return b.MinPeers
}

func (b *Bootstrap) GetPeerCount() int {
	return b.PeerCount
}

//Bootstrap thought the list of bootstrap peer's
func (b *Bootstrap) Bootstrap(ctx context.Context) error {

	if !b.startedState.HasStarted() {
		return errors.New("you need to to call Start() first in order to manually bootstrap")
	}

	if len(b.bootstrapPeers) < b.MinPeers {
		return errors.New("number of configured peers is less than minimum required to bootstrap")
	}

	var e error

	var wg sync.WaitGroup

	for _, peer := range b.bootstrapPeers {

		wg.Add(1)
		go func(peer *peerstore.PeerInfo) {
			defer wg.Done()
			b.PeerCount = b.peerState.Amount()
			if b.PeerCount < b.MinPeers {
				if err := b.host.Connect(ctx, *peer); err != nil {
					glog.Infof("Contacting %s", peer)
					e = err
					return
				}
				glog.Infof("Connected to %s", peer)
			}
		}(peer)

	}

	wg.Wait()

	b.PeerCount = b.peerState.Amount()
	if b.PeerCount >= b.MinPeers {
		return nil
	}

	return e
}

//Stop the bootstrap service
func (b *Bootstrap) Close() error {
	if !b.startedState.HasStarted() {
		return errors.New("bootstrap must be started in order to stop it")
	}

	b.host.Network().StopNotify(b.notifiee)
	b.startedState.Stop()

	// close the ticker
	b.hardBootstrap.closer <- struct{}{}
	b.bootstrap.closer <- struct{}{}

	return nil
}

//Start bootstrapping
func (b *Bootstrap) Start(ctx context.Context) error {

	//Pre start conditions
	if b.startedState.HasStarted() {
		return errors.New("already started")
	}
	b.startedState.Start()

	//Set initial amount of peer's
	b.peerState.SetAmountOfPeers(len(b.host.Network().Peers()))

	//Listener that updates the amount of connected peer's
	notifyBundle := net.NotifyBundle{
		DisconnectedF: func(network net.Network, conn net.Conn) {
			b.peerState.SetAmountOfPeers(len(network.Peers()))
		},
		ConnectedF: func(network net.Network, conn net.Conn) {
			b.peerState.SetAmountOfPeers(len(network.Peers()))
		},
	}
	b.host.Network().Notify(&notifyBundle)

	//Do an initial bootstrap
	err := b.Bootstrap(ctx)

	// hard bootstrap
	go func() {

		for {
			select {
			case <-b.hardBootstrap.closer:
				return
			case <-b.hardBootstrap.ticker.C:
				b.PeerCount = b.peerState.Amount()

				// return when we are connected to enough peers
				if b.PeerCount >= b.MinPeers {
					continue
				}

				if err := b.Bootstrap(ctx); err != nil {
					glog.Error(err)
				}
			}
		}

	}()

	// normal bootstrap
	go func() {

		lastNetworkState := len(b.host.Network().Peers())

		for {
			select {
			case <-b.bootstrap.closer:
				return
			case <-b.bootstrap.ticker.C:

				myAddresses := len(b.host.Network().Peers())

				b.PeerCount = b.peerState.Amount()
				//Continue when we are connected to the minPeer amount
				if b.PeerCount >= b.MinPeers {
					continue
				}

				// bootstrap on network delta (delta between the amount
				// of our addresses and the last known amount of addresses)
				if myAddresses != lastNetworkState {
					lastNetworkState = myAddresses
					if err := b.Bootstrap(ctx); err != nil {
						glog.Error(err)
					}
				}
			}
		}

	}()

	return err

}

//Create new bootstrap service
func New(h host.Host, c Config) (*Bootstrap, error) {

	if c.MinPeers > len(c.BootstrapPeers) {
		return nil, errors.New(fmt.Sprintf("Too few bootstrapping nodes. Expected at least: %d, got: %d", c.MinPeers, len(c.BootstrapPeers)))
	}

	var peers []*peerstore.PeerInfo

	for _, v := range c.BootstrapPeers {
		addr, err := ma.NewMultiaddr(v)

		if err != nil {
			return nil, err
		}

		pInfo, err := peerstore.InfoFromP2pAddr(addr)

		if err != nil {
			return nil, err
		}

		peers = append(peers, pInfo)
	}

	return &Bootstrap{
		MinPeers:       c.MinPeers,
		bootstrapPeers: peers,
		host:           h,
		hardBootstrap: wrappedTicker{
			ticker: time.NewTicker(c.HardBootstrap),
			closer: make(chan struct{}),
		},
		bootstrap: wrappedTicker{
			ticker: time.NewTicker(c.BootstrapInterval),
			closer: make(chan struct{}),
		},
		startedState: startedState.StateFactory(),
		peerState:    peerState.StateFactory(),
	}, nil

}
