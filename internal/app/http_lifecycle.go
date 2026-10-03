package app

import (
 "context"
 "net/http"
)

// Admission and the wait counter share a lock: once shutdown begins, no new
// handler can start using SQLite while Run waits for the accepted requests.
func (a *App) trackRequests(next http.Handler) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  a.requestMu.Lock()
  if a.stopping {a.requestMu.Unlock();http.Error(w,"server shutting down",http.StatusServiceUnavailable);return}
  a.requests.Add(1);a.requestMu.Unlock();defer a.requests.Done()
  ctx,cancel:=context.WithCancel(r.Context());stop:=context.AfterFunc(a.requestContext,cancel)
  defer stop();defer cancel()
  next.ServeHTTP(w,r.WithContext(ctx))
 })
}
func (a *App) stopRequests() {
 a.requestMu.Lock();a.stopping=true;a.requestMu.Unlock()
 a.cancelRequests()
}
