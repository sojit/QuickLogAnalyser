package archive

import (
 "archive/tar"
 "bytes"
 "context"
 "io"
 "testing"
 "quickanalyser/internal/config"
)

// Audit-only regression probe. This file is not present in the user's source.
func TestAuditBudgetCountsSkippedPayload(t *testing.T) {
 var buf bytes.Buffer
 tw:=tar.NewWriter(&buf)
 payload:=bytes.Repeat([]byte("x"),8192)
 if err:=tw.WriteHeader(&tar.Header{Name:"large.bin",Mode:0600,Size:int64(len(payload))});err!=nil{t.Fatal(err)}
 if _,err:=tw.Write(payload);err!=nil{t.Fatal(err)}
 if err:=tw.Close();err!=nil{t.Fatal(err)}
 limits:=config.DefaultLimits();limits.MaxExpandedBytes=1024
 exceeded:=false
 w:=Walker{Limits:limits,OnBudgetExceed:func(string){exceeded=true}}
 err:=w.Walk(context.Background(),bytes.NewReader(buf.Bytes()),"probe.tar",1,func(_ context.Context,m Member,r io.Reader)error{
  if r!=nil{_,_=io.Copy(io.Discard,io.LimitReader(r,64))};return nil
 })
 if err==nil&&!exceeded{t.Fatalf("8192-byte payload passed a 1024-byte expansion budget; only %d bytes counted",w.ExpandedBytes)}
}
