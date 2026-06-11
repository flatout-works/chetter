# ConnectRPC Handler Reference

## Handler Registration (main.go)

```go
import (
    "golang.org/x/net/http2"
    "golang.org/x/net/http2/h2c"
)

mux := http.NewServeMux()
mux.Handle(thingv1connect.NewThingServiceHandler(
    thingSvc,
    connect.WithInterceptors(middleware.NewAuthInterceptor(cfg)),
))

server := &http.Server{
    Addr:    ":" + cfg.Port,
    Handler: h2c.NewHandler(mux, &http2.Server{}),
}
```

`h2c` (HTTP/2 Cleartext) allows gRPC-style streaming without TLS termination inside the container.

## Interceptor Pattern

```go
func NewAuthInterceptor(cfg *config.Config) connect.UnaryInterceptorFunc {
    return func(next connect.UnaryFunc) connect.UnaryFunc {
        return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
            // Validate JWT from Authorization header
            // Set user ID into context on success
            return next(ctx, req)
        }
    }
}
```

## Streaming

connect-go supports both unary and streaming RPCs. For server streaming:

```go
func (s *Service) StreamEvents(ctx context.Context,
    req *connect.Request[eventv1.StreamEventsRequest],
    stream *connect.ServerStream[eventv1.StreamEventsResponse]) error {
    for event := range events {
        if err := stream.Send(&eventv1.StreamEventsResponse{Event: event}); err != nil {
            return err
        }
    }
    return nil
}
```
