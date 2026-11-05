package enginefs

import "testing"

func TestLockExcludesSecondHolder(t *testing.T) {
	dir := t.TempDir()

	first, err := Lock(dir)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	if second, err := Lock(dir); err == nil {
		_ = second.Close()

		t.Fatal("second lock acquired while the first is held")
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := Lock(dir)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}

	_ = again.Close()
}
