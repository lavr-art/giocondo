package deploy

import "testing"

func TestFilesEmbedded(t *testing.T) {
	for _, name := range []string{"install-agent.sh"} {
		data, err := Files.ReadFile(name)
		if err != nil || len(data) == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
}
