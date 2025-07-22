package amqpjobs

import (
	"sync"
	"time"
)

type listener struct {
	wg   *sync.WaitGroup
	time time.Duration
}
