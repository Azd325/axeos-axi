package axeos

import (
	"os"
	"testing"

	"github.com/Azd325/axeos-axi/internal/nettest"
)

func TestMain(m *testing.M) {
	os.Exit(nettest.Run(m))
}
