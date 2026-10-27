package jobs
import("archive/tar";"bytes";"compress/gzip";"os";"path/filepath";"testing";"time";"quickanalyser/internal/config";"quickanalyser/internal/storage")
func TestLifecycleAuditRepeatedUploadAndReopen(t *testing.T){
 var b bytes.Buffer;gz:=gzip.NewWriter(&b);tw:=tar.NewWriter(gz)
 payload:=[]byte("2026-10-23 11:38:00 ERROR lifecycle-marker\n")
 if err:=tw.WriteHeader(&tar.Header{Name:"var/log/app.log",Mode:0600,Size:int64(len(payload))});err!=nil{t.Fatal(err)}
 tw.Write(payload);tw.Close();gz.Close()
 root,err:=storage.OpenTestRoot(t.TempDir());if err!=nil{t.Fatal(err)};defer root.Close()
 e:=NewEngine(root,config.DefaultLimits())
 wait:=func(id string){
  until:=time.Now().Add(20*time.Second)
  for time.Now().Before(until){
   if e.ActiveJobID()=="" {return};time.Sleep(20*time.Millisecond)
  };t.Fatal("job did not become idle")
 }
 first,err:=e.CreateJob("sample.tar.gz",0,bytes.NewReader(b.Bytes()));if err!=nil{t.Fatal(err)};wait(first)
 retained:=filepath.Join(root.JobDir(first),"input","snapshot.bin")
 stored,err:=os.ReadFile(retained);if err!=nil{t.Fatal(err)}
 if !bytes.Equal(stored,b.Bytes()){t.Fatal("retained upload differs")}
 reopened:=NewEngine(root,config.DefaultLimits());rows,err:=reopened.ListJobs();if err!=nil||len(rows)!=1{t.Fatalf("reopen failed: %d %v",len(rows),err)}
 t.Log("same-root reopen found existing saved analysis; uploaded archive copy retained on disk")
 second,err:=reopened.CreateJob("renamed-same-bytes.tgz",0,bytes.NewReader(b.Bytes()));if err!=nil{t.Fatal(err)};wait(second)
 rows,err=reopened.ListJobs();if err!=nil{t.Fatal(err)}
 t.Logf("first=%s second=%s saved analyses=%d",first,second,len(rows))
 if second!=first || len(rows)!=1{t.Fatalf("identical upload reanalysed rather than reused: %d jobs",len(rows))}
}
