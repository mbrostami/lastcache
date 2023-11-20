# LastCache
LastCache is a go module that implements a resilient in-memory cache. It prevents calling service to be failed if the caller is irresponsible.  

e.g. In microservice architecture, when there is a need for synchronous call. Last cache will be helpful to have resiliency.  

#### example
```go
package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/mbrostami/lastcache"
)

func main() {

	lc := lastcache.New(lastcache.Config{
		GlobalTTL: 1*time.Nanosecond,
	})
	//////////////////////
	// successful callback
	val, err := lc.LoadOrStore("key", func(key any) (any, bool, error) {
		// return new value
		return "value", false, nil
	})
	fmt.Printf("callback healthy, %+v, err: %v\n", val, err)
	
	
	// wait for cache to be expired
	time.Sleep(2*time.Nanosecond)


	////////////////////////////////////
	// failed callback, using last cache
	// the cache is expired but fails to get fresh data
	val, err = lc.LoadOrStore("key", func(key any) (any, bool, error) {
		// return err and use last available cache
		return nil, true, errors.New("service unavailable")
	})
	fmt.Printf("callback failed, use cache, %+v, err: %v\n", val, err)

	////////////////////////////////////////
	// failed callback, not using last cache
	// the cache is expired but failure to get fresh data, not using cache
	val, err = lc.LoadOrStore("key", func(key any) (any, bool, error) {
		// return err and not use last cache
		return nil, false, errors.New("service unavailable")
	})
	fmt.Printf("callback failed, don't use cache, %+v, err: %v\n", val, err)
}

```