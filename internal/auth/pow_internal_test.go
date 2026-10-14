package auth

import (
	"strconv"
	"testing"
)

func TestPoWSpentStaysBoundedAndKeepsWorking(t *testing.T) {
	var p PoWSpent
	const now = 1000
	p.spend("expired", now-1, now)
	for i := 1; i < powSpentCap; i++ {
		p.spend("live"+strconv.Itoa(i), now+60, now)
	}
	if !p.spend("new", now+60, now) {
		t.Fatal("full set refused a fresh solution")
	}
	if _, ok := p.salts["expired"]; ok {
		t.Fatal("expired entry kept")
	}
	if !p.spend("newer", now+60, now) || len(p.salts) > powSpentCap {
		t.Fatalf("set grew to %d", len(p.salts))
	}
	if p.spend("newer", now+60, now) {
		t.Fatal("spent twice")
	}
}
