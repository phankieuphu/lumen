package app

import (
	"reflect"
	"testing"
)

// lifecycle lists App methods the shell calls itself; the UI must not.
var lifecycle = map[string]bool{"Close": true, "SetEmitter": true}

// TestAPIMatchesApp keeps API in step with App: every UI method is
// forwarded with the same signature, and lifecycle methods stay hidden.
func TestAPIMatchesApp(t *testing.T) {
	appT := reflect.TypeFor[*App]()
	apiT := reflect.TypeFor[*API]()

	for i := range appT.NumMethod() {
		m := appT.Method(i)
		got, ok := apiT.MethodByName(m.Name)
		switch {
		case lifecycle[m.Name] && ok:
			t.Errorf("API exposes lifecycle method %s", m.Name)
		case lifecycle[m.Name]:
		case !ok:
			t.Errorf("API is missing %s; forward it or add it to lifecycle", m.Name)
		case !sameSignature(m.Type, got.Type):
			t.Errorf("API.%s is %v, App.%s is %v", m.Name, got.Type, m.Name, m.Type)
		}
	}
	for i := range apiT.NumMethod() {
		if name := apiT.Method(i).Name; !hasMethod(appT, name) {
			t.Errorf("API.%s has no App counterpart", name)
		}
	}
}

func hasMethod(t reflect.Type, name string) bool {
	_, ok := t.MethodByName(name)
	return ok
}

// sameSignature compares method types without the receiver.
func sameSignature(a, b reflect.Type) bool {
	if a.NumIn() != b.NumIn() || a.NumOut() != b.NumOut() {
		return false
	}
	for i := 1; i < a.NumIn(); i++ {
		if a.In(i) != b.In(i) {
			return false
		}
	}
	for i := range a.NumOut() {
		if a.Out(i) != b.Out(i) {
			return false
		}
	}
	return true
}
