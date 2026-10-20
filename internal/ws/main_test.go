package ws

import (
	"os"
	"testing"

	"github.com/Azd325/axeos-axi/internal/nettest"
)

func TestMain(m *testing.M) {
	DialControl = nettest.Control
	os.Exit(nettest.Run(m))
}
