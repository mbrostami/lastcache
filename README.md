# LastCache
LastCache is a go module that implements stale-while-revalidate and stale-if-error in-memory cache strategy.   

### stale-if-error
In the event of an error when fetching fresh data, the cache serves stale (expired) data for a specified period (Config.ExtendTTL). This ensures a fallback mechanism to provide some data even when the retrieval process encounters errors.  
`LoadOrStore` function is based on this strategy.  

### stale-while-revalidate
Stale (expired) data is served to caller while a background process runs to refresh the cache.      
`AsyncLoadOrStore` function is based on this strategy.


### Examples
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
		GlobalTTL      : 1*time.Minute,
		ExtendTTL      : 10*time.Second,
		AsyncSemaphore : 1,
	})
	/////////////////////////////////////////////////////
	////////////////// stale-if-error ///////////////////
	// successful callback
	val, err := lc.LoadOrStore("key", func(key any) (value any, useStale bool, err error) {
		return "value", false, nil
	})
	fmt.Printf("sync, %+v, err: %v\n", val, err)

	
	// failed callback - use stale
	val, err = lc.LoadOrStore("key", func(key any) (value any, useStale bool, err error) {
		return nil, true, errors.New("connection lost")
	})
	fmt.Printf("sync, %+v, err: %v\n", val, err)

	
	// failed callback - do not use stale
	val, err = lc.LoadOrStore("key", func(key any) (value any, useStale bool, err error) {
		return nil, false, errors.New("resource not found")
	})
	fmt.Printf("sync, %+v, err: %v\n", val, err)


	/////////////////////////////////////////////////////
	///////////////// stale-while-revalidate ////////////
	// successful callback
	val, errChannel, err := lc.AsyncLoadOrStore("key", func(key any) (value any, err error) {
		return "value", nil
	})
	callbackErr := <-errChannel
	fmt.Printf("async, %+v, callback:%v, err: %v\n", val, callbackErr, err)

	
	// failed callback
	val, errChannel, err = lc.AsyncLoadOrStore("key", func(key any) (value any, err error) {
		return nil, errors.New("some query error")
	})
	callbackErr = <-errChannel
	fmt.Printf("async, %+v, callback:%v, err: %v\n", val, callbackErr, err)

}

```