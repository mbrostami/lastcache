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

		callback func(key any) (any, error)
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    any
		wantErr bool
	}{
		{
			name: "callback with error valid cache",
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
				callback: func(key any) (any, error) {
					return nil, errors.New("unavailable")
				},
			},
			want:    "value",
			wantErr: false,
		},
		{
			name: "expired cache, callback with new value",
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
				callback: func(key any) (any, error) {
					return "value2", nil
				},
			},
			want:    "value2",
			wantErr: false,
		},
		{
			name: "non expired cache, callback with new value",
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
				callback: func(key any) (any, error) {
					return "value2", nil
				},
			},
			want:    "value",
			wantErr: false,
		},

		{
			name: "non expired cache, callback with new value",
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
				callback: func(key any) (any, error) {
					return "value2", nil
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
			if !reflect.DeepEqual(got, tt.want) {
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
		callback func(key any) (any, error)
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    any
		wantErr bool
	}{
		{
			name: "callback with err",
			fields: fields{
				config: Config{
					GlobalTTL: 10 * time.Millisecond,
				},
			},
			args: args{
				key:   "storeKey",
				value: "value",
				callback: func(key any) (any, error) {
					return nil, errors.New("unavailable")
				},
			},
			want:    "value",
			wantErr: false,
		},
		{
			name: "callback with no err",
			fields: fields{
				config: Config{
					GlobalTTL: 10 * time.Millisecond,
				},
			},
			args: args{
				key:   "storeKey",
				value: "value",
				callback: func(key any) (any, error) {
					return "value", nil
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
			c.Set(tt.args.key, tt.args.value)

			got, err := c.LoadOrStore(tt.args.key, tt.args.callback)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadOrStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
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
		callback  func(key any) (any, error)
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    any
		wantErr bool
	}{
		{
			name: "callback with err",
			fields: fields{
				config: Config{
					GlobalTTL: 10 * time.Millisecond,
				},
			},
			args: args{
				storeKey:  "storeKey",
				lookupKey: "key2",
				value:     "value",
				callback: func(key any) (any, error) {
					return nil, errors.New("unavailable")
				},
			},
			wantErr: true,
		},
		{
			name: "callback with err",
			fields: fields{
				config: Config{
					GlobalTTL: 10 * time.Millisecond,
				},
			},
			args: args{
				storeKey:  "storeKey",
				lookupKey: "key2",
				value:     "value",
				callback: func(key any) (any, error) {
					return "value for key2", nil
				},
			},
			want:    "value for key2",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Cache{
				config: tt.fields.config,
			}
			c.Set(tt.args.storeKey, tt.args.value)

			got, err := c.LoadOrStore(tt.args.lookupKey, tt.args.callback)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadOrStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
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
		callback func(key any) (any, error)
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    any
		wantErr bool
	}{
		{
			name: "callback with error non existing cache",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Millisecond,
				},
			},
			args: args{
				key: "storeKey",
				callback: func(key any) (any, error) {
					return nil, errors.New("unavailable")
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "callback no error",
			fields: fields{
				config: Config{
					GlobalTTL: 1 * time.Millisecond,
				},
			},
			args: args{
				key: "storeKey",
				callback: func(key any) (any, error) {
					return "value", nil
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
			if !reflect.DeepEqual(got, tt.want) {
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
		afterTime  func() time.Time
		callback   func(key any) (any, error)
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
			name: "testA",
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
				callback: func(key any) (any, error) {
					nrCalls++
					return nil, errors.New("unavailable")
				},
			},
			want:        "value",
			wantNrCalls: 1,
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

			now = tt.args.afterTime

			nrCalls = 0
			// read from callback
			got, err := c.LoadOrStore(tt.args.key, tt.args.callback)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadOrStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LoadOrStore() got = %v, want %v", got, tt.want)
			}

			// read from cache
			c.LoadOrStore(tt.args.key, tt.args.callback)

			if nrCalls != tt.wantNrCalls {
				t.Errorf("Number of callback calls got = %v, want %v", nrCalls, tt.wantNrCalls)
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
				c.LoadOrStore(key, func(key any) (any, error) {
					return value, nil
				})
				c.TTL(key)
				c.Delete(key)
				wg.Done()
			}()
		}
		wg.Wait()
	})
}

func BenchmarkLoadOrStore(b *testing.B) {
	c := New(Config{GlobalTTL: 1 * time.Millisecond})
	c.Set("key", "value")
	for i := 0; i < b.N; i++ {
		g, _ := c.LoadOrStore("key", func(key any) (any, error) {
			return "value", nil
		})
		if g != "value" {
			b.Errorf("got %v, want %v", g, "value")
		}
	}
}
