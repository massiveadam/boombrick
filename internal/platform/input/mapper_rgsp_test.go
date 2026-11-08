package input

import "testing"

func TestRGSPButtons(t *testing.T) {
	m := Mapper{Buttons: RGSPButtons}
	for b, want := range map[uint8]Button{0: A, 1: B, 2: Y, 3: X, 4: L1, 5: R1, 6: Select, 7: Start, 8: Menu, 9: L2, 10: R2} {
		ev := m.JoyButton(b, true)
		if len(ev) != 1 || ev[0].Button != want || ev[0].Kind != Press {
			t.Errorf("button %d: %v, want %v", b, ev, want)
		}
	}
	if ev := m.JoyButton(13, true); ev != nil {
		t.Errorf("button 13 mapped: %v", ev)
	}
	// The Brick's numbering is unchanged without a layout.
	if ev := (&Mapper{}).JoyButton(1, true); ev[0].Button != A {
		t.Errorf("Brick button 1: %v", ev)
	}
}
