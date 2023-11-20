[![Go Report Card](https://goreportcard.com/badge/github.com/mbrostami/lastcache)](https://goreportcard.com/report/github.com/mbrostami/lastcache)
![Coverage](https://img.shields.io/badge/Coverage-96.7%25-brightgreen)
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
	"context"

	"github.com/mbrostami/lastcache"
)

func main() {


	var callbackErr error
	lc := lastcache.New(lastcache.Config{
		GlobalTTL:      1 * time.Nanosecond, // 1 * time.Minute,
		ExtendTTL:      1 * time.Nanosecond, // 10 * time.Second,
		AsyncSemaphore: 1,
	})
	/////////////////////////////////////////////////////
	////////////////// stale-if-error ///////////////////
	// successful callback
	val, err := lc.LoadOrStore("key", func(ctx context.Context, key any) (value any, useStale bool, err error) {
		return "value", false, nil
	})
	fmt.Printf("sync, \tValue: %s, \tStale: %v, \tCallbackErr: %v, \terr: %v\n", val.Value, val.Stale, val.Err, err)

	// failed callback - use stale
	val, err = lc.LoadOrStore("key", func(ctx context.Context, key any) (value any, useStale bool, err error) {
		return nil, true, errors.New("connection lost")
	})
	fmt.Printf("sync, \tValue: %s, \tStale: %v, \tCallbackErr: %v, \terr: %v\n", val.Value, val.Stale, val.Err, err)

	// failed callback - do not use stale
	val, err = lc.LoadOrStore("key", func(ctx context.Context, key any) (value any, useStale bool, err error) {
		return nil, false, errors.New("resource not found")
	})
	fmt.Printf("sync, \tValue: %+v, \terr: %v\n", val, err)

	/////////////////////////////////////////////////////
	///////////////// stale-while-revalidate ////////////
	// successful callback
	val, errChannel, err := lc.AsyncLoadOrStore("key_2", func(ctx context.Context, key any) (value any, err error) {
		return "value", nil
	})

	if errChannel != nil { // check callback error
		callbackErr = <-errChannel
	}
	fmt.Printf("async, \tValue: %s, \tStale: %v, \tCallbackErr: %v, \terr: %v\n", val.Value, val.Stale, callbackErr, err)

	// failed callback
	val, errChannel, err = lc.AsyncLoadOrStore("key_2", func(ctx context.Context, key any) (value any, err error) {
		return nil, errors.New("some query error")
	})
	if errChannel != nil { // check callback error
		callbackErr = <-errChannel
	}
	fmt.Printf("async, \tValue: %s, \tStale: %v, \tCallbackErr: %v, \terr: %v\n", val.Value, val.Stale, callbackErr, err)
}

```

Output: 
```
sync, 	Value: value, 	Stale: false, 	CallbackErr: <nil>, 	err: <nil>
sync, 	Value: value, 	Stale: true, 	CallbackErr: connection lost, 	err: <nil>
sync, 	Value: <nil>, 	err: resource not found
async, 	Value: value, 	Stale: false, 	CallbackErr: <nil>, 	err: <nil>
async, 	Value: value, 	Stale: true, 	CallbackErr: some query error, 	err: <nil>
```