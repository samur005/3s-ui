package cronjob

import (
	"sync"

	"github.com/samur005/3s-ui/service"
)

type ClusterIPLimitJob struct {
	running sync.Mutex
}

func NewClusterIPLimitJob() *ClusterIPLimitJob {
	return &ClusterIPLimitJob{}
}

func (j *ClusterIPLimitJob) Run() {
	if !j.running.TryLock() {
		return
	}
	defer j.running.Unlock()
	service.EnforceClusterIPLimits()
}
