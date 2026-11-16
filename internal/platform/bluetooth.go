package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// Bluetooth asks BlueZ, over the system D-Bus, which audio device is
// connected, for the status bar, and connects and disconnects the headsets
// and speakers already paired, for the quick menu. The firmware's own
// settings pair them. Every call can wait on the bus: never from the UI
// thread.
type Bluetooth struct {
	sys   string // the sysfs tree the adapters are listed in
	mu    sync.Mutex
	conn  *dbus.Conn
	retry time.Time // when to ask again after BlueZ or the bus failed
}

// retryBus is how long a missing bus or BlueZ is left before asking again.
const retryBus = time.Minute

// NewBluetooth returns a Bluetooth that asks BlueZ only while the sysfs tree
// at sys, normally "/sys", lists an adapter.
func NewBluetooth(sys string) *Bluetooth { return &Bluetooth{sys: sys} }

// Connected is the name of the connected Bluetooth audio device, or "" when
// none is connected or BlueZ isn't there to ask.
func (b *Bluetooth) Connected() string {
	objects, err := b.objects()
	if err != nil {
		return ""
	}
	return connectedAudio(objects)
}

// ErrNoBluetooth is Devices' error when there is no adapter, as with
// Bluetooth off, or BlueZ isn't there to ask.
var ErrNoBluetooth = errors.New("bluetooth is off")

// BTDevice is a paired headset or speaker.
type BTDevice struct {
	Addr      string // its address, as AA:BB:CC:DD:EE:FF
	Name      string // the name the user gave it, or its own
	Connected bool
	path      dbus.ObjectPath
}

// Devices lists the paired audio devices, by name.
func (b *Bluetooth) Devices() ([]BTDevice, error) {
	objects, err := b.objects()
	if err != nil {
		return nil, err
	}
	return pairedAudio(objects), nil
}

// connectWait is how long BlueZ is given to connect a headset: one out of
// reach takes its page timeout, about five seconds, and a profile or two more.
const connectWait = 20 * time.Second

// SetConnected connects the paired device at addr, after disconnecting any
// other audio device connected (one plays at a time), or disconnects it. It
// then asks plorpOS's radio.sh for a pass at once (/tmp/tortos_btpass), so
// the output follows within a second, not at its next twenty-second pass;
// there, a headset disconnected is left alone until it is connected again.
func (b *Bluetooth) SetConnected(addr string, on bool) error {
	devs, err := b.Devices()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(devs, func(d BTDevice) bool { return strings.EqualFold(d.Addr, addr) })
	if i < 0 {
		return errors.New("not paired")
	}
	defer passSoon()
	b.mu.Lock()
	conn := b.conn
	b.mu.Unlock()
	if conn == nil {
		return ErrNoBluetooth
	}
	call := func(d BTDevice, method string) error {
		ctx, cancel := context.WithTimeout(context.Background(), connectWait)
		defer cancel()
		return conn.Object("org.bluez", d.path).CallWithContext(ctx, "org.bluez.Device1."+method, 0).Err
	}
	if !on {
		return call(devs[i], "Disconnect")
	}
	for j, d := range devs {
		if j != i && d.Connected {
			_ = call(d, "Disconnect") // the one being connected still gets its go
		}
	}
	if err := call(devs[i], "Connect"); err != nil {
		var e dbus.Error
		if errors.As(err, &e) && strings.HasSuffix(e.Name, ".AlreadyConnected") {
			return nil
		}
		return err
	}
	return nil
}

// passSoon asks radio.sh's bt_reconnect for a pass now, as plorpOS's own
// Bluetooth screen does (src/bt.c publish_soon).
func passSoon() {
	if f, err := os.OpenFile("/tmp/tortos_btpass", os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
		f.Close()
	}
}

// objects is BlueZ's object tree, or ErrNoBluetooth.
func (b *Bluetooth) objects() (map[dbus.ObjectPath]map[string]map[string]dbus.Variant, error) {
	if !hasAdapter(b.sys) {
		return nil, ErrNoBluetooth // nothing to connect with, as when Bluetooth is off
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Now().Before(b.retry) {
		return nil, ErrNoBluetooth
	}
	if b.conn == nil {
		conn, err := dbus.ConnectSystemBus()
		if err != nil {
			b.retry = time.Now().Add(retryBus)
			return nil, ErrNoBluetooth
		}
		b.conn = conn
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := b.conn.Object("org.bluez", "/").CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects)
	if err != nil {
		// An error the bus sent back, as when BlueZ isn't running, leaves the
		// connection fine to ask again; anything else, as the bus going away,
		// doesn't.
		if !replied(err) {
			b.conn.Close()
			b.conn = nil
		}
		b.retry = time.Now().Add(retryBus)
		return nil, ErrNoBluetooth
	}
	return objects, nil
}

// Close lets go of the bus.
func (b *Bluetooth) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		b.conn.Close()
		b.conn = nil
	}
}

// replied reports whether err is an error reply from the bus or a service on
// it.
func replied(err error) bool {
	var e dbus.Error
	return errors.As(err, &e)
}

// hasAdapter reports whether the sysfs tree at sys lists a Bluetooth adapter.
func hasAdapter(sys string) bool {
	entries, _ := os.ReadDir(filepath.Join(sys, "class", "bluetooth"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "hci") {
			return true
		}
	}
	return false
}

// connectedAudio picks the connected audio device from BlueZ's objects:
// headphones, a headset or a speaker, by the icon BlueZ gives it, in path
// order. Its alias is the name the user gave it, or its own.
func connectedAudio(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant) string {
	paths := make([]dbus.ObjectPath, 0, len(objects))
	for p := range objects {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		dev, ok := objects[p]["org.bluez.Device1"]
		if !ok {
			continue
		}
		connected, _ := dev["Connected"].Value().(bool)
		icon, _ := dev["Icon"].Value().(string)
		if !connected || !strings.HasPrefix(icon, "audio-") {
			continue
		}
		for _, key := range []string{"Alias", "Name"} {
			if name, _ := dev[key].Value().(string); name != "" {
				return name
			}
		}
	}
	return ""
}

// pairedAudio is the paired audio devices in BlueZ's objects, by the icon
// BlueZ gives them, sorted by name.
func pairedAudio(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant) []BTDevice {
	var out []BTDevice
	for p, ifaces := range objects {
		dev, ok := ifaces["org.bluez.Device1"]
		if !ok {
			continue
		}
		paired, _ := dev["Paired"].Value().(bool)
		icon, _ := dev["Icon"].Value().(string)
		if !paired || !strings.HasPrefix(icon, "audio-") {
			continue
		}
		d := BTDevice{path: p}
		d.Addr, _ = dev["Address"].Value().(string)
		d.Connected, _ = dev["Connected"].Value().(bool)
		for _, key := range []string{"Alias", "Name"} {
			if name, _ := dev[key].Value().(string); name != "" {
				d.Name = name
				break
			}
		}
		if d.Name == "" {
			d.Name = d.Addr
		}
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b BTDevice) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.Addr, b.Addr)
	})
	return out
}
