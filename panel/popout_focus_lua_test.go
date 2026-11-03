package panel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// fakeHL is a recording stand-in for Hyprland's `hl` Lua table: calls lands in
// order, noWarps is the live cursor.no_warps, and raiseOnFocus makes the
// focus dispatch raise a Lua error.
type fakeHL struct {
	calls        []string
	noWarps      bool
	raiseOnFocus bool
	pointer      *[2]float64
}

// runPopOutFocus evaluates the generated Lua (from the real launchers.js under
// Node) against the fake hl and runs it, returning the fake's record.
func runPopOutFocus(t *testing.T, fake *fakeHL, addr string, x, w, grabY, monY, monH int) {
	t.Helper()
	expr := fmt.Sprintf("popOutFocusDispatch(%s, %d, %d, %d, %d, %d)", jsStringLiteral(addr), x, w, grabY, monY, monH)
	var src string
	if err := json.Unmarshal(evalJS(t, expr, "js/launchers.js"), &src); err != nil {
		t.Fatalf("decode generated Lua: %v", err)
	}

	L := lua.NewState()
	defer L.Close()
	hl := L.NewTable()
	L.SetField(hl, "get_config", L.NewFunction(func(L *lua.LState) int {
		fake.calls = append(fake.calls, "get_config "+L.CheckString(1))
		L.Push(lua.LBool(fake.noWarps))
		return 1
	}))
	L.SetField(hl, "config", L.NewFunction(func(L *lua.LState) int {
		v := L.CheckTable(1).RawGetString("cursor").(*lua.LTable).RawGetString("no_warps")
		fake.noWarps = lua.LVAsBool(v)
		fake.calls = append(fake.calls, fmt.Sprintf("config no_warps=%v", fake.noWarps))
		return 0
	}))
	L.SetField(hl, "dispatch", L.NewFunction(func(L *lua.LState) int {
		d := L.CheckTable(1)
		op := d.RawGetString("op").String()
		if op == "focus" {
			fake.calls = append(fake.calls, "focus")
			if fake.raiseOnFocus {
				L.RaiseError("focus failed")
			}
			return 0
		}
		fake.calls = append(fake.calls, fmt.Sprintf("cursor.move %v,%v", d.RawGetString("x"), d.RawGetString("y")))
		return 0
	}))
	L.SetField(hl, "get_cursor_pos", L.NewFunction(func(L *lua.LState) int {
		if fake.pointer == nil {
			L.Push(lua.LNil)
			return 1
		}
		p := L.NewTable()
		L.SetField(p, "x", lua.LNumber(fake.pointer[0]))
		L.SetField(p, "y", lua.LNumber(fake.pointer[1]))
		L.Push(p)
		return 1
	}))
	dsp := L.NewTable()
	L.SetField(dsp, "focus", L.NewFunction(func(L *lua.LState) int {
		d := L.NewTable()
		L.SetField(d, "op", lua.LString("focus"))
		L.Push(d)
		return 1
	}))
	cur := L.NewTable()
	L.SetField(cur, "move", L.NewFunction(func(L *lua.LState) int {
		a := L.CheckTable(1)
		d := L.NewTable()
		L.SetField(d, "op", lua.LString("move"))
		L.SetField(d, "x", a.RawGetString("x"))
		L.SetField(d, "y", a.RawGetString("y"))
		L.Push(d)
		return 1
	}))
	L.SetField(dsp, "cursor", cur)
	L.SetField(hl, "dsp", dsp)
	L.SetGlobal("hl", hl)

	if err := L.DoString("return " + src); err != nil {
		t.Fatalf("generated Lua does not load: %v\n%s", err, src)
	}
	fn := L.Get(-1)
	if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}); err != nil {
		t.Fatalf("generated Lua raised: %v", err)
	}
}

func moves(calls []string) []string {
	var out []string
	for _, c := range calls {
		if strings.HasPrefix(c, "cursor.move") {
			out = append(out, c)
		}
	}
	return out
}

func TestPopOutFocusLuaRestoresNoWarpsOnEveryPath(t *testing.T) {
	for _, before := range []bool{false, true} {
		for _, raise := range []bool{false, true} {
			fake := &fakeHL{noWarps: before, raiseOnFocus: raise}
			runPopOutFocus(t, fake, "0xbb22", 100, 400, 50, 0, 1000)
			if fake.noWarps != before {
				t.Errorf("before=%v raise=%v: no_warps ended %v; calls %v", before, raise, fake.noWarps, fake.calls)
			}
			want := []string{"get_config cursor.no_warps", "config no_warps=true", "focus", fmt.Sprintf("config no_warps=%v", before)}
			if strings.Join(fake.calls, "|") != strings.Join(want, "|") {
				t.Errorf("before=%v raise=%v: calls %v, want %v", before, raise, fake.calls, want)
			}
		}
	}
}

func TestPopOutFocusLuaMovesPointerOnlyOnTheWindowsMonitor(t *testing.T) {
	// Window x 100..500 on a monitor spanning y 1080..2160 (a second monitor
	// stacked above it spans y 0..1080).
	cases := []struct {
		name    string
		pointer *[2]float64
		want    []string
	}{
		{"inside x range and on the monitor", &[2]float64{250, 1500}, []string{"cursor.move 250,1200"}},
		{"matching x on the monitor above", &[2]float64{250, 500}, nil},
		{"matching x on a monitor below", &[2]float64{250, 2500}, nil},
		{"left of the x range", &[2]float64{99, 1500}, nil},
		{"right of the x range", &[2]float64{500, 1500}, nil},
		{"no pointer position", nil, nil},
	}
	for _, c := range cases {
		fake := &fakeHL{pointer: c.pointer}
		runPopOutFocus(t, fake, "0xbb22", 100, 400, 1200, 1080, 1080)
		got := moves(fake.calls)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: moves %v, want %v", c.name, got, c.want)
		}
	}
}
