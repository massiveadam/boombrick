package app

import (
	"errors"
	"sync"

	"github.com/ujjwal313/boombrick/internal/platform"
	"github.com/ujjwal313/boombrick/internal/screens/quickmenu"
)

// Bluetooth is the paired headsets and speakers, to connect and disconnect
// from the quick menu. *platform.Bluetooth is one. Both calls can wait on
// the radio for seconds.
type Bluetooth interface {
	Devices() ([]platform.BTDevice, error)
	SetConnected(addr string, on bool) error
}

// btMenu keeps the quick menu's Bluetooth list: read and changed on
// goroutines of its own, the UI thread only ever reads the copy kept here.
// changed is called, from those goroutines, each time it moves.
type btMenu struct {
	bt      Bluetooth
	changed func()

	mu      sync.Mutex
	list    quickmenu.BTList
	reading bool
}

// List is the list as last read.
func (m *btMenu) List() quickmenu.BTList {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.list
}

// Refresh reads the list again, unless a read or a change is under way.
func (m *btMenu) Refresh() {
	m.mu.Lock()
	if m.reading || m.list.Busy != "" {
		m.mu.Unlock()
		return
	}
	m.reading = true
	m.mu.Unlock()
	go func() {
		m.read()
		m.changed()
	}()
}

// read reads the list, on the caller's goroutine.
func (m *btMenu) read() {
	devs, err := m.bt.Devices()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reading = false
	m.list.Loaded = true
	m.list.Off = errors.Is(err, platform.ErrNoBluetooth)
	m.list.Devices = m.list.Devices[:0:0]
	if err != nil {
		return
	}
	for _, d := range devs {
		m.list.Devices = append(m.list.Devices, quickmenu.BTDevice{Addr: d.Addr, Name: d.Name, Connected: d.Connected})
	}
}

// Toggle connects the device at addr, or disconnects it when it is
// connected; one at a time.
func (m *btMenu) Toggle(addr string) {
	m.mu.Lock()
	if m.list.Busy != "" {
		m.mu.Unlock()
		return
	}
	on := true
	for _, d := range m.list.Devices {
		if d.Addr == addr {
			on = !d.Connected
		}
	}
	m.list.Busy, m.list.Failed = addr, ""
	m.mu.Unlock()
	m.changed()
	go func() {
		err := m.bt.SetConnected(addr, on)
		m.read()
		m.mu.Lock()
		m.list.Busy = ""
		if err != nil {
			m.list.Failed = addr
		}
		m.mu.Unlock()
		m.changed()
	}()
}
