package lastcache

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

var fixedTime = func() time.Time {
	return time.Unix(1000, 0)
}

func TestCache_Set_LoadOrStore_Expired(t *testing.T) {
	type fields struct {
		config Config
	}
	type args struct {
		key        any
		value      any
		beforeTime func() time.Time
		afterTime  func() time.Time

		callback func(key any) (any, bool, error)
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    any
		wantErr bool
	}{
		{
			name: "syncCallback with error valid cache",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Millisecond,
				},
			},
			args: args{
				key:        "storeKey",
				value:      "value",
				beforeTime: func() time.Time { return fixedTime() },
				afterTime:  func() time.Time { return fixedTime().Add(10 * time.Millisecond) },
				callback: func(key any) (any, bool, error) {
					return nil, true, errors.New("unavailable")
				},
			},
			want:    "value",
			wantErr: false,
		},
		{
			name: "expired cache, syncCallback with new value",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Millisecond,
				},
			},
			args: args{
				key:        "storeKey",
				value:      "value",
				beforeTime: func() time.Time { return fixedTime() },
				afterTime:  func() time.Time { return fixedTime().Add(10 * time.Millisecond) },
				callback: func(key any) (any, bool, error) {
					return "value2", false, nil
				},
			},
			want:    "value2",
			wantErr: false,
		},
		{
			name: "non expired cache, syncCallback with new value",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Second,
				},
			},
			args: args{
				key:        "storeKey",
				value:      "value",
				beforeTime: func() time.Time { return fixedTime() },
				afterTime:  func() time.Time { return fixedTime().Add(10 * time.Millisecond) },
				callback: func(key any) (any, bool, error) {
					return "value2", false, nil
				},
			},
			want:    "value",
			wantErr: false,
		},

		{
			name: "non expired cache, syncCallback with new value",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Second,
				},
			},
			args: args{
				key:        "storeKey",
				value:      "value",
				beforeTime: func() time.Time { return fixedTime() },
				afterTime:  func() time.Time { return fixedTime().Add(10 * time.Millisecond) },
				callback: func(key any) (any, bool, error) {
					return "value2", false, nil
				},
			},
			want:    "value",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cache{
				config: tt.fields.config,
			}
			now = tt.args.beforeTime

			c.Set(tt.args.key, tt.args.value)

			now = tt.args.afterTime

			got, err := c.LoadOrStore(tt.args.key, tt.args.callback)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadOrStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got.Value, tt.want) {
				t.Errorf("LoadOrStore() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCache_Set_LoadOrStore_NonExpired(t *testing.T) {
	type fields struct {
		config Config
	}
	type args struct {
		key      any
		value    any
		callback func(key any) (any, bool, error)
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    any
		wantErr bool
	}{
		{
			name: "syncCallback with err using last cache",
			fields: fields{
				config: Config{
					GlobalTTL: 10 * time.Millisecond,
				},
			},
			args: args{
				key:   "storeKey",
				value: "value",
				callback: func(key any) (any, bool, error) {
					return nil, true, errors.New("unavailable")
				},
			},
			want:    "value",
			wantErr: false,
		},
		{
			name: "syncCallback with err not using last cache",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Nanosecond,
				},
			},
			args: args{
				key:   "storeKey",
				value: "value",
				callback: func(key any) (any, bool, error) {
					return nil, false, errors.New("unavailable")
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "syncCallback with no err",
			fields: fields{
				config: Config{
					GlobalTTL: 10 * time.Millisecond,
				},
			},
			args: args{
				key:   "storeKey",
				value: "value",
				callback: func(key any) (any, bool, error) {
					return "value", false, nil
				},
			},
			want:    "value",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cache{
				config: tt.fields.config,
			}
			now = func() time.Time { return fixedTime() }
			c.Set(tt.args.key, tt.args.value)
			now = func() time.Time {
				return fixedTime().Add(tt.fields.config.GlobalTTL + 1)
			}
			got, err := c.LoadOrStore(tt.args.key, tt.args.callback)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadOrStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != nil && !reflect.DeepEqual(got.Value, tt.want) {
				t.Errorf("LoadOrStore() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCache_Set_LoadOrStore_InvalidKey(t *testing.T) {
	type fields struct {
		config Config
	}
	type args struct {
		storeKey  any
		lookupKey any
		value     any
		callback  func(key any) (any, bool, error)
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    Entry
		wantErr bool
	}{
		{
			name: "syncCallback with err",
			fields: fields{
				config: Config{
					GlobalTTL: 10 * time.Millisecond,
				},
			},
			args: args{
				storeKey:  "storeKey",
				lookupKey: "key2",
				value:     "value",
				callback: func(key any) (any, bool, error) {
					return nil, false, errors.New("unavailable")
				},
			},
			wantErr: true,
		},
		{
			name: "syncCallback with err",
			fields: fields{
				config: Config{
					GlobalTTL: 10 * time.Millisecond,
				},
			},
			args: args{
				storeKey:  "storeKey",
				lookupKey: "key2",
				value:     "value",
				callback: func(key any) (any, bool, error) {
					return "value for key2", false, nil
				},
			},
			want:    Entry{Value: "value for key2"},
			wantErr: false,
		},
		{
			name: "syncCallback with err use last cache",
			fields: fields{
				config: Config{
					GlobalTTL: 10 * time.Millisecond,
				},
			},
			args: args{
				storeKey:  "key",
				lookupKey: "key",
				value:     "value",
				callback: func(key any) (any, bool, error) {
					return nil, true, errors.New("unavailable")
				},
			},
			want:    Entry{Value: "value", Stale: true, Err: errors.New("unavailable")},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cache{
				config: tt.fields.config,
			}
			now = func() time.Time { return fixedTime() }
			c.Set(tt.args.storeKey, tt.args.value)

			// expire the key
			now = func() time.Time {
				return fixedTime().Add(tt.fields.config.GlobalTTL + 1)
			}

			got, err := c.LoadOrStore(tt.args.lookupKey, tt.args.callback)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadOrStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != nil && !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("LoadOrStore() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCache_LoadOrStore(t *testing.T) {
	type fields struct {
		config Config
	}
	type args struct {
		key      any
		callback func(key any) (any, bool, error)
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    any
		wantErr bool
	}{
		{
			name: "syncCallback with error non existing cache",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Millisecond,
				},
			},
			args: args{
				key: "storeKey",
				callback: func(key any) (any, bool, error) {
					return nil, false, errors.New("unavailable")
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "syncCallback no error",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Millisecond,
				},
			},
			args: args{
				key: "storeKey",
				callback: func(key any) (any, bool, error) {
					return "value", false, nil
				},
			},
			want:    "value",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cache{
				config: tt.fields.config,
			}
			got, err := c.LoadOrStore(tt.args.key, tt.args.callback)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadOrStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != nil && !reflect.DeepEqual(got.Value, tt.want) {
				t.Errorf("LoadOrStore() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCache_LoadOrStore_NrCalls(t *testing.T) {
	type fields struct {
		config Config
	}
	nrCalls := 0
	type args struct {
		key        any
		value      any
		beforeTime func() time.Time
		firstTime  func() time.Time
		secondTime func() time.Time
		callback   func(key any) (any, bool, error)
	}
	tests := []struct {
		name        string
		fields      fields
		args        args
		want        any
		wantNrCalls int
		wantErr     bool
	}{
		{
			name: "use stale cache without extended ttl",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Millisecond,
				},
			},
			args: args{
				key:        "storeKey",
				value:      "value",
				beforeTime: func() time.Time { return fixedTime() },
				firstTime:  func() time.Time { return fixedTime().Add(10 * time.Millisecond) },
				callback: func(key any) (any, bool, error) {
					nrCalls++
					return nil, true, errors.New("unavailable")
				},
			},
			want:        "value",
			wantNrCalls: 2, // as extendedTTL is not set
			wantErr:     false,
		},
		{
			name: "use stale cache with extended ttl",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Millisecond,
					ExtendTTL: 12 * time.Millisecond,
				},
			},
			args: args{
				key:        "storeKey",
				value:      "value",
				beforeTime: func() time.Time { return fixedTime() },
				firstTime:  func() time.Time { return fixedTime().Add(10 * time.Millisecond) },
				callback: func(key any) (any, bool, error) {
					nrCalls++
					return nil, true, errors.New("unavailable")
				},
			},
			want:        "value",
			wantNrCalls: 1, // as extendedTTL is used, the second call will not execute the callback
			wantErr:     false,
		},
		{
			name: "use stale cache with extended ttl but expired again",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Millisecond,
					ExtendTTL: 5 * time.Millisecond,
				},
			},
			args: args{
				key:        "storeKey",
				value:      "value",
				beforeTime: func() time.Time { return fixedTime() },
				firstTime:  func() time.Time { return fixedTime().Add(10 * time.Millisecond) },
				secondTime: func() time.Time { return fixedTime().Add(16 * time.Millisecond) },
				callback: func(key any) (any, bool, error) {
					nrCalls++
					return nil, true, errors.New("unavailable")
				},
			},
			want:        "value",
			wantNrCalls: 2, // as extendedTTL is used but expired before the second call
			wantErr:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cache{
				config: tt.fields.config,
			}
			now = tt.args.beforeTime
			c.Set(tt.args.key, tt.args.value)

			now = tt.args.firstTime

			nrCalls = 0
			// read from syncCallback
			got, err := c.LoadOrStore(tt.args.key, tt.args.callback)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadOrStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != nil && !reflect.DeepEqual(got.Value, tt.want) {
				t.Errorf("LoadOrStore() got = %v, want %v", got, tt.want)
			}

			if tt.args.secondTime != nil {
				now = tt.args.secondTime
			}

			// read from cache
			c.LoadOrStore(tt.args.key, tt.args.callback)

			if nrCalls != tt.wantNrCalls {
				t.Errorf("Number of syncCallback calls got = %v, want %v", nrCalls, tt.wantNrCalls)
			}
		})
	}
}

func TestCache_Expiry(t *testing.T) {
	type fields struct {
		config Config
	}

	type args struct {
		storeKey   any
		lookupKey  any
		value      any
		beforeTime func() time.Time
		afterTime  func() time.Time
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   time.Duration
	}{
		{
			name: "expired 9ms ago",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Millisecond,
				},
			},
			args: args{
				storeKey:   "storeKey",
				lookupKey:  "storeKey",
				value:      "value",
				beforeTime: func() time.Time { return fixedTime() },
				afterTime:  func() time.Time { return fixedTime().Add(10 * time.Millisecond) },
			},
			want: -9 * time.Millisecond,
		},
		{
			name: "expired 9s ago",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Second,
				},
			},
			args: args{
				storeKey:   "storeKey",
				lookupKey:  "storeKey",
				value:      "value",
				beforeTime: func() time.Time { return fixedTime() },
				afterTime:  func() time.Time { return fixedTime().Add(10 * time.Second) },
			},
			want: -9 * time.Second,
		},
		{
			name: "not expire yet",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Second,
				},
			},
			args: args{
				storeKey:   "storeKey",
				lookupKey:  "storeKey",
				value:      "value",
				beforeTime: func() time.Time { return fixedTime() },
				afterTime:  func() time.Time { return fixedTime().Add(10 * time.Millisecond) },
			},
			want: 990 * time.Millisecond,
		},
		{
			name: "not expire yet",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Second,
				},
			},
			args: args{
				storeKey:   "storeKey",
				lookupKey:  "nonExistingKey",
				value:      "value",
				beforeTime: func() time.Time { return fixedTime() },
				afterTime:  func() time.Time { return fixedTime().Add(10 * time.Millisecond) },
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cache{
				config: tt.fields.config,
			}

			now = tt.args.beforeTime
			c.Set(tt.args.storeKey, tt.args.value)

			now = tt.args.afterTime
			got := c.TTL(tt.args.lookupKey)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LoadOrStore() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCache_Delete(t *testing.T) {
	type fields struct {
		config Config
	}

	type args struct {
		key   any
		value any
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name: "delete key and lookup",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Millisecond,
				},
			},
			args: args{
				key:   "storeKey",
				value: "value",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cache{
				config: tt.fields.config,
			}

			c.Set(tt.args.key, tt.args.value)

			c.Delete(tt.args.key)

			_, ok := c.mapStorage.Load(tt.args.key)
			if !reflect.DeepEqual(ok, tt.want) {
				t.Errorf("LoadOrStore() got = %v, want %v", ok, tt.want)
			}
			_, ok = c.timeStorage.Load(tt.args.key)
			if !reflect.DeepEqual(ok, tt.want) {
				t.Errorf("LoadOrStore() got = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestNew(t *testing.T) {
	type args struct {
		config Config
	}
	tests := []struct {
		name string
		args args
		want Config
	}{
		{
			name: "default ttl",
			args: args{
				config: Config{},
			},
			want: Config{
				GlobalTTL: defaultTTL,
			},
		},
		{
			name: "config with ttl",
			args: args{
				config: Config{
					GlobalTTL: 10 * time.Second,
				},
			},
			want: Config{
				GlobalTTL: 10 * time.Second,
			},
		},
		{
			name: "config with negative ttl",
			args: args{
				config: Config{
					GlobalTTL: -10 * time.Second,
				},
			},
			want: Config{
				GlobalTTL: defaultTTL,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New(tt.args.config); !reflect.DeepEqual(got.config, tt.want) {
				t.Errorf("New() = %v, want %v", got.config, tt.want)
			}
		})
	}
}

func TestCache_LoadOrStore_Race(t *testing.T) {
	t.Run("race test", func(t *testing.T) {
		c := New(Config{})
		wg := sync.WaitGroup{}
		wg.Add(100)
		key := "key"
		value := "value"
		for i := 0; i < 100; i++ {
			go func() {
				c.Set(key, value)
				c.LoadOrStore(key, func(key any) (any, bool, error) {
					return value, false, nil
				})
				c.TTL(key)
				c.Delete(key)
				wg.Done()
			}()
		}
		wg.Wait()
	})
}

func TestCache_AsyncLoadOrStoreNonExistingKey(t *testing.T) {
	key := "key"
	val := "value"

	callback := func(key any) (value any, err error) {
		return val, nil
	}

	cache := New(Config{
		GlobalTTL: 10 * time.Millisecond,
	})

	now = func() time.Time { return fixedTime() }

	entry, _, err := cache.AsyncLoadOrStore(key, callback)
	if err != nil {
		t.Errorf("failed with err: %v", err)
	}

	if entry.Value != val {
		t.Errorf("entry Value got %v, want %v", entry.Value, val)
	}

	if entry.Stale == true {
		t.Errorf("entry Stale expected to be false, true returned")
	}
}

func TestCache_AsyncLoadOrStoreNonExistingKeyWithError(t *testing.T) {
	key := "key"

	callback := func(key any) (value any, err error) {
		return nil, errors.New("not found")
	}

	cache := New(Config{
		GlobalTTL: 10 * time.Millisecond,
	})

	now = func() time.Time { return fixedTime() }

	entry, _, err := cache.AsyncLoadOrStore(key, callback)
	if err == nil {
		t.Errorf("want err, got nil")
	}

	if entry != nil {
		t.Errorf("want nil entry, got %+v", entry)
	}
}

func TestCache_AsyncLoadOrStore(t *testing.T) {
	key := "key"
	val := "value"

	callback := func(key any) (value any, err error) {
		time.Sleep(5 * time.Millisecond)
		return "new_value", nil
	}

	cache := New(Config{
		GlobalTTL:      10 * time.Millisecond,
		ExtendTTL:      10 * time.Millisecond,
		AsyncSemaphore: 1,
	})

	//////////// time 0
	now = func() time.Time { return fixedTime() }

	cache.Set(key, val)

	//////////// time 1
	// GlobalTTL + 1 makes cache expired
	now = func() time.Time { return fixedTime().Add(11 * time.Millisecond) }

	entry, ch, err := cache.AsyncLoadOrStore(key, callback)
	if err != nil {
		t.Errorf("failed with err: %v", err)
	}

	if entry.Value != val {
		t.Errorf("entry Value got %v, want %v", entry.Value, val)
	}

	if entry.Stale == false {
		t.Errorf("entry Stale expected to be true, false returned")
	}

	//////////// time 2
	// 11 + 5(callback time) + 1
	<-ch
	now = func() time.Time { return fixedTime().Add(17 * time.Millisecond) }

	entry, _, err = cache.AsyncLoadOrStore(key, callback)
	if err != nil {
		t.Errorf("failed with err: %v", err)
	}

	if entry.Value != "new_value" {
		t.Errorf("entry Value got %v, want new_value", entry.Value)
	}

	if entry.Stale == true {
		t.Errorf("entry Stale expected to be false, true returned")
	}
}

func TestCache_AsyncLoadOrStoreConcurrentOneSemaphore(t *testing.T) {
	key := "key"
	val := "value"

	callbackFirst := func(key any) (value any, err error) {
		return "new_value_1", nil
	}

	callbackSecond := func(key any) (value any, err error) {
		return "new_value_2", nil
	}

	cache := New(Config{
		GlobalTTL:      10 * time.Millisecond,
		ExtendTTL:      10 * time.Millisecond,
		AsyncSemaphore: 1,
	})

	//////////// time 0
	now = func() time.Time { return fixedTime() }

	cache.Set(key, val)

	//////////// time 1
	// GlobalTTL + 1 makes cache expired
	now = func() time.Time { return fixedTime().Add(11 * time.Millisecond) }

	// first call
	entry, ch1, err := cache.AsyncLoadOrStore(key, callbackFirst)
	if err != nil {
		t.Errorf("failed with err: %v", err)
	}

	if entry.Value != val {
		t.Errorf("entry Value got %v, want %v", entry.Value, val)
	}

	if entry.Stale == false {
		t.Errorf("entry Stale expected to be true, false returned")
	}

	// second call
	var ch2 chan error
	entry, ch2, err = cache.AsyncLoadOrStore(key, callbackSecond)
	if err != nil {
		t.Errorf("failed with err: %v", err)
	}

	if entry.Value != val {
		t.Errorf("entry Value got %v, want %v", entry.Value, val)
	}

	if entry.Stale == false {
		t.Errorf("entry Stale expected to be true, false returned")
	}

	//////////// time 2
	// 11 + 5(callback time) + 1
	<-ch1
	<-ch2 // to avoid rc in tests because of `now`
	now = func() time.Time { return fixedTime().Add(17 * time.Millisecond) }

	entry, _, err = cache.AsyncLoadOrStore(key, callbackFirst)
	if err != nil {
		t.Errorf("failed with err: %v", err)
	}

	if entry.Value != "new_value_1" && entry.Value != "new_value_2" { // second callback should not run because the first one is already updated the cache
		t.Errorf("entry Value got %v, want new_value_1 or new_value_2", entry.Value)
	}

	if entry.Stale == true {
		t.Errorf("entry Stale expected to be false, true returned")
	}
}

func TestCache_AsyncLoadOrStoreConcurrentTwoSemaphore(t *testing.T) {
	key := "key"
	val := "value"

	callbackFirst := func(key any) (value any, err error) {
		time.Sleep(20 * time.Millisecond) // make this slower than second callback
		return "new_value_1", nil
	}

	callbackSecond := func(key any) (value any, err error) {
		return "new_value_2", nil
	}

	cache := New(Config{
		GlobalTTL:      10 * time.Millisecond,
		ExtendTTL:      10 * time.Millisecond,
		AsyncSemaphore: 2,
	})

	//////////// time 0
	now = func() time.Time { return fixedTime() }

	cache.Set(key, val)

	//////////// time 1
	// GlobalTTL + 1 makes cache expired
	now = func() time.Time { return fixedTime().Add(11 * time.Millisecond) }

	entry, ch1, err := cache.AsyncLoadOrStore(key, callbackFirst)
	if err != nil {
		t.Errorf("failed with err: %v", err)
	}

	if entry.Value != val {
		t.Errorf("entry Value got %v, want %v", entry.Value, val)
	}

	if entry.Stale == false {
		t.Errorf("entry Stale expected to be true, false returned")
	}

	// second call
	var ch2 chan error
	entry, ch2, err = cache.AsyncLoadOrStore(key, callbackSecond)
	if err != nil {
		t.Errorf("failed with err: %v", err)
	}

	if entry.Value != val {
		t.Errorf("entry Value got %v, want %v", entry.Value, val)
	}

	if entry.Stale == false {
		t.Errorf("entry Stale expected to be true, false returned")
	}

	//////////// time 2
	// 11 + 5(callback time) + 1
	<-ch2 // wait for second call
	<-ch1 // wait for first call
	now = func() time.Time { return fixedTime().Add(17 * time.Millisecond) }

	entry, _, err = cache.AsyncLoadOrStore(key, callbackFirst)
	if err != nil {
		t.Errorf("failed with err: %v", err)
	}

	if entry.Value != "new_value_2" { // two callbacks run at the same time
		t.Errorf("entry Value got %v, want new_value_2", entry.Value)
	}

	if entry.Stale == true {
		t.Errorf("entry Stale expected to be false, true returned")
	}
}

func BenchmarkLoadOrStore(b *testing.B) {
	c := New(Config{GlobalTTL: 1 * time.Millisecond})
	c.Set("key", "value")
	for i := 0; i < b.N; i++ {
		g, _ := c.LoadOrStore("key", func(key any) (any, bool, error) {
			return "value", false, nil
		})
		if g.Value != "value" {
			b.Errorf("got %v, want %v", g, "value")
		}
	}
}

func BenchmarkAsyncLoadOrStore(b *testing.B) {
	c := New(Config{GlobalTTL: 1 * time.Millisecond})
	c.Set("key", "value")
	for i := 0; i < b.N; i++ {
		g, _, _ := c.AsyncLoadOrStore("key", func(key any) (any, error) {
			return "value", nil
		})
		if g.Value != "value" {
			b.Errorf("got %v, want %v", g, "value")
		}
	}
}
