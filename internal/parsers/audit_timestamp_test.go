package parsers
import("strings";"testing")
func TestAuditPreserveFullTimestamp(t *testing.T){
 stamp:="2026-10-23T11:38:03.456+04:00"
 events,err:=ParseText(strings.NewReader(stamp+" ERROR failed\n"))
 if err!=nil || len(events)!=1 {t.Fatalf("parse: %v %d",err,len(events))}
 if events[0].RawTime!=stamp {t.Fatalf("timestamp loses precision/zone: got %q want %q",events[0].RawTime,stamp)}
}
