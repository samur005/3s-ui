package service

import (
	"os"
	"testing"

	"github.com/samur005/3s-ui/logger"

	"github.com/op/go-logging"
)

// TestMain initialises the 3s-ui logger before any test runs. Several paths here
// report through it -- the stats buffer when it has to drop undelivered rows,
// the reset job, the node reconcile -- and an uninitialised logger panics.
func TestMain(m *testing.M) {
	logger.InitLogger(logging.ERROR)
	os.Exit(m.Run())
}
