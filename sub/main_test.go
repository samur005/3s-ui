package sub

import (
	"os"
	"testing"

	"github.com/samur005/3s-ui/logger"

	"github.com/op/go-logging"
)

// TestMain initialises the 3s-ui logger before any test runs. The subscription
// builders report a malformed stored link through it, and an uninitialised
// logger panics.
func TestMain(m *testing.M) {
	logger.InitLogger(logging.ERROR)
	os.Exit(m.Run())
}
