package platform

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// device is a BlueZ device object as GetManagedObjects lists it.
func device(alias, name, icon string, connected bool) map[string]map[string]dbus.Variant {
	props := map[string]dbus.Variant{
		"Connected": dbus.MakeVariant(connected),
		"Icon":      dbus.MakeVariant(icon),
	}
	if alias != "" {
		props["Alias"] = dbus.MakeVariant(alias)
	}
	if name != "" {
		props["Name"] = dbus.MakeVariant(name)
	}
	return map[string]map[string]dbus.Variant{"org.bluez.Device1": props}
}

func TestConnectedAudio(t *testing.T) {
	objects := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		"/org/bluez/hci0":                    {"org.bluez.Adapter1": {}},
		"/org/bluez/hci0/dev_11_22_33_44_55": device("Pixel", "Pixel 9", "phone", true),
		"/org/bluez/hci0/dev_AA_BB_CC_DD_EE": device("Kite Buds", "KB-200", "audio-headset", true),
		"/org/bluez/hci0/dev_FF_EE_DD_CC_BB": device("Old Speaker", "", "audio-card", false),
	}
	if got := connectedAudio(objects); got != "Kite Buds" {
		t.Fatalf("connectedAudio = %q, want Kite Buds", got)
	}
	// Without an alias, the device's own name.
	objects["/org/bluez/hci0/dev_AA_BB_CC_DD_EE"] = device("", "KB-200", "audio-headphones", true)
	if got := connectedAudio(objects); got != "KB-200" {
		t.Fatalf("connectedAudio = %q, want KB-200", got)
	}
	// A phone connected alone isn't where the music goes.
	delete(objects, "/org/bluez/hci0/dev_AA_BB_CC_DD_EE")
	if got := connectedAudio(objects); got != "" {
		t.Fatalf("connectedAudio = %q with no audio device connected", got)
	}
}

// pairedAudio lists the paired headsets and speakers by name, connected or
// not, and leaves out what isn't paired or isn't audio.
func TestPairedAudio(t *testing.T) {
	paired := func(addr, alias, icon string, isPaired, connected bool) map[string]map[string]dbus.Variant {
		d := device(alias, "", icon, connected)
		d["org.bluez.Device1"]["Address"] = dbus.MakeVariant(addr)
		d["org.bluez.Device1"]["Paired"] = dbus.MakeVariant(isPaired)
		return d
	}
	objects := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		"/org/bluez/hci0":                    {"org.bluez.Adapter1": {}},
		"/org/bluez/hci0/dev_11_22_33_44_55": paired("11:22:33:44:55:66", "Pixel", "phone", true, true),
		"/org/bluez/hci0/dev_AA_BB_CC_DD_EE": paired("AA:BB:CC:DD:EE:FF", "kite Buds", "audio-headset", true, true),
		"/org/bluez/hci0/dev_FF_EE_DD_CC_BB": paired("FF:EE:DD:CC:BB:AA", "Filbert", "audio-headphones", true, false),
		"/org/bluez/hci0/dev_00_00_00_00_00": paired("00:00:00:00:00:01", "Seen once", "audio-card", false, false),
	}
	got := pairedAudio(objects)
	if len(got) != 2 || got[0].Name != "Filbert" || got[0].Connected || got[1].Addr != "AA:BB:CC:DD:EE:FF" || !got[1].Connected {
		t.Fatalf("pairedAudio = %+v", got)
	}
	if got[0].path != "/org/bluez/hci0/dev_FF_EE_DD_CC_BB" {
		t.Fatalf("path %q", got[0].path)
	}
}

// withAdapter is a sysfs tree that lists a Bluetooth adapter.
func withAdapter(t *testing.T) string {
	return sysfs(t, map[string]string{"bluetooth/hci0/name": "hci0"})
}

// Without a system bus, or with no BlueZ on it, there is nothing to show, and
// it is asked again only after a while.
func TestNoBluetoothService(t *testing.T) {
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path="+t.TempDir()+"/no-bus")
	b := NewBluetooth(withAdapter(t))
	defer b.Close()
	start := time.Now()
	if got := b.Connected(); got != "" {
		t.Fatalf("Connected = %q without a bus", got)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v to find no bus", d)
	}
	if b.retry.Before(time.Now().Add(30 * time.Second)) {
		t.Fatalf("asks again at %v, want a while off", b.retry)
	}
}

// With no adapter listed, as with Bluetooth off, the bus isn't asked at all.
func TestNoAdapterAsksNothing(t *testing.T) {
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path="+t.TempDir()+"/no-bus")
	b := NewBluetooth(sysfs(t, map[string]string{"net/wlan0/operstate": "up"}))
	defer b.Close()
	if got := b.Connected(); got != "" || !b.retry.IsZero() || b.conn != nil {
		t.Fatalf("Connected = %q, retry %v, connected %v; want the bus left alone", got, b.retry, b.conn != nil)
	}
}

func TestReplied(t *testing.T) {
	unknown := dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"}
	for _, c := range []struct {
		err  error
		want bool
	}{
		{unknown, true},
		{fmt.Errorf("ask BlueZ: %w", unknown), true},
		{dbus.ErrClosed, false},
		{context.DeadlineExceeded, false},
		{io.EOF, false},
	} {
		if got := replied(c.err); got != c.want {
			t.Errorf("replied(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// BlueZ missing from a bus that is there, as with Bluetooth off, keeps the
// connection for the next time; the bus going away drops it. It needs
// dbus-daemon.
func TestAnErrorReplyKeepsTheConnection(t *testing.T) {
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("no dbus-daemon")
	}
	addr := "unix:path=" + filepath.Join(t.TempDir(), "bus")
	cmd := exec.Command(daemon, "--session", "--nofork", "--nopidfile", "--print-address", "--address="+addr)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			cmd.Process.Kill()
			cmd.Wait()
		}
	}
	defer stop()
	if _, err := bufio.NewReader(out).ReadString('\n'); err != nil {
		t.Fatalf("dbus-daemon didn't start: %v", err)
	}
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", addr)

	b := NewBluetooth(withAdapter(t))
	defer b.Close()
	if got := b.Connected(); got != "" || b.conn == nil || b.retry.IsZero() {
		t.Fatalf("Connected = %q, connected %v, retry %v; want the connection kept and a wait", got, b.conn != nil, b.retry)
	}
	conn := b.conn
	b.retry = time.Time{}
	b.Connected()
	if b.conn != conn {
		t.Fatal("asked again on a new connection")
	}
	stop()
	b.retry = time.Time{}
	b.Connected()
	if b.conn != nil {
		t.Fatal("the connection was kept after the bus went away")
	}
}
