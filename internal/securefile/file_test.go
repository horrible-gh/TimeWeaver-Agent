package securefile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrivateAtomicReplace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "conf", "credential.json")
	for _, value := range []string{"one", "two"} {
		if e := Write(p, []byte(value), true); e != nil {
			t.Fatal(e)
		}
		b, e := ReadPrivate(p)
		if e != nil || string(b) != value {
			t.Fatal(string(b), e)
		}
	}
	i, _ := os.Stat(p)
	if runtime.GOOS != "windows" && i.Mode().Perm() != 0600 {
		t.Fatal(i.Mode())
	}
}
func TestInstanceLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent.lock")
	one, e := Acquire(p)
	if e != nil {
		t.Fatal(e)
	}
	if two, e := Acquire(p); e == nil {
		two.Close()
		t.Fatal("second lock acquired")
	}
	one.Close()
	three, e := Acquire(p)
	if e != nil {
		t.Fatal(e)
	}
	three.Close()
}
func TestRefuseSymlinkState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	_ = os.WriteFile(target, []byte("keep"), 0600)
	link := filepath.Join(dir, "link")
	if e := os.Symlink(target, link); e != nil {
		t.Fatal(e)
	}
	if e := Write(link, []byte("bad"), true); e == nil {
		t.Fatal("followed symlink")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "keep" {
		t.Fatal("target changed")
	}
}
