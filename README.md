# LastCache
LastCache is a go module that implements a resilient in-memory cache.  

e.g. In microservice architecture, when there is a need for synchronous call,
last cache will be helpful to have resiliency.  

```go

import (
	"fmt"
	
	"github.com/mbrostami/lastcache"
)

func main() {
    
    lc := lastcache.New(lastcache.Config{
        GlobalTTL: 10*time.Second,
    }) 

    val, err := lc.LoadOrStore("key", func() any, error {
        // return err to use last available cache
        val, err := s2s_call()
        return val, err
    })
	
    if err != nil {
       panic(err)	
    }
    fmt.Println(val)
}
```