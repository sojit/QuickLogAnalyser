package classify
import "testing"
func TestAuditCSVAndStructuredSettings(t *testing.T) {
 cases:=[]struct{name,path,body string}{
  {"empty_syslog_settings_csv","NX/conf/extSyslogServers.csv",""},
  {"syslog_settings_csv","NX/conf/extSyslogServers.csv","host,port,protocol\n192.0.2.1,514,udp\n"},
  {"json_settings_in_log_dir","NX/log/settings.json","{\"retry\":3,\"endpoint\":\"localhost\"}\n"},
 }
 for _,c:=range cases {t.Run(c.name,func(t *testing.T){
  r:=ClassifyMember(c.path,[]byte(c.body),false)
  if r.Kind==KindLog {t.Errorf("non-log source classified as log: %s (%s)",r.Kind,r.Reason)}
 })}
}
func TestAuditGenuineCSVLogRetained(t *testing.T) {
 r:=ClassifyMember("snapshot/records/events.csv",[]byte("2026-10-23 10:00:00,ERROR,connection refused\n2026-10-23 10:01:00,WARN,link down\n"),false)
 if r.Kind!=KindLog {t.Fatalf("genuine CSV event log lost: %+v",r)}
}
