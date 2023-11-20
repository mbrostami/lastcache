package lastcache_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mbrostami/lastcache"
)

func Test(t *testing.T) {

	var callbackErr error
	lc := lastcache.New(lastcache.Config{
		GlobalTTL:      1 * time.Nanosecond, // 1 * time.Minute,
		ExtendTTL:      1 * time.Nanosecond, // 10 * time.Second,
		AsyncSemaphore: 1,
	})
	/////////////////////////////////////////////////////
	////////////////// stale-if-error ///////////////////
	// successful callback
	val, err := lc.LoadOrStore("key", func(key any) (value any, useStale bool, err error) {
		return "value", false, nil
	})
	fmt.Printf("sync, \tValue: %s, \tStale: %v, \tCallbackErr: %v, \terr: %v\n", val.Value, val.Stale, val.Err, err)

	// failed callback - use stale
	val, err = lc.LoadOrStore("key", func(key any) (value any, useStale bool, err error) {
		return nil, true, errors.New("connection lost")
	})
	fmt.Printf("sync, \tValue: %s, \tStale: %v, \tCallbackErr: %v, \terr: %v\n", val.Value, val.Stale, val.Err, err)

	// failed callback - do not use stale
	val, err = lc.LoadOrStore("key", func(key any) (value any, useStale bool, err error) {
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
