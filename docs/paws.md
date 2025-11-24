# PAWS - PawScript Asynchronous Wait System

```go
ps.RegisterCommand("fetch_data", func(ctx *Context) Result {
    url := fmt.Sprintf("%v", ctx.Args[0])
    token := ctx.RequestToken(nil)
    
    // Hand control back to host application
    go func() {
        // Host does its async work (network call, database query, etc.)
        data, err := myHostApp.FetchData(url)
        
        if err == nil {
            ctx.SetResult(data)
            ctx.ResumeToken(token, true)
        } else {
            ctx.SetResult("")
            ctx.ResumeToken(token, false)
        }
    }()
    
    return TokenResult(token)  // Suspend here
})
```
