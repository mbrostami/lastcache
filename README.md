# LastCache
LastCache is a go module that implements a resilient in-memory cache.  

e.g. In microservice architecture, when there is a need for synchronous call,
last cache will be helpful to have resiliency.  

#### 3 cases example
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
	
	
	
	time.Sleep(2*time.Nanosecond)


	////////////////////////////////////
	// failed callback, using last cache
	// cache is expired but failure to get fresh data
	val, err = lc.LoadOrStore("key", func(key any) (any, bool, error) {
		// return err and use last available cache
		return nil, true, errors.New("service unavailable")
	})
	fmt.Printf("callback failed, use cache, %+v, err: %v\n", val, err)

	////////////////////////////////////////
	// failed callback, not using last cache
	// cache is expired but failure to get fresh data, not using cache
	val, err = lc.LoadOrStore("key", func(key any) (any, bool, error) {
		// return err and not use last cache
		return nil, false, errors.New("service unavailable")
	})
	fmt.Printf("callback failed, don't use cache, %+v, err: %v\n", val, err)
}

```