package detection

import (
 "fmt"
 "strings"
 "testing"
)

func TestAuditFindingLimitMustSignalPartial(t *testing.T) {
 var b strings.Builder
 for i:=0;i<305;i++ {fmt.Fprintf(&b,"2026-10-23 10:00:00 ERROR event %d\n",i)}
 findings,err:=ScanReader(strings.NewReader(b.String()))
 if len(findings)<305 && err==nil {t.Fatalf("silently retained %d of 305 findings with nil error",len(findings))}
}
func TestAuditRecentTailMustNotDisappearSilently(t *testing.T) {
 text:=strings.Repeat("2023-01-01 00:00:00 INFO ordinary historical entry\n",50000)+"2026-10-23 11:38:00 ERROR latest-tail-marker\n"
 findings,err:=ScanReader(strings.NewReader(text))
 found:=false
 for _,f:=range findings {if strings.Contains(f.Message,"latest-tail-marker"){found=true}}
 if !found && err==nil {t.Fatal("recent tail finding omitted without partial/error signal")}
}
func TestAuditDetectionTimestampPreservesZone(t *testing.T) {
 stamp:="2026-10-23T11:38:03.456+04:00"
 findings,err:=ScanReader(strings.NewReader(stamp+" ERROR failed\n"))
 if err!=nil || len(findings)!=1 {t.Fatalf("scan: %v %d",err,len(findings))}
 if findings[0].RawTime!=stamp {t.Fatalf("timestamp loses precision/zone: %q",findings[0].RawTime)}
}
