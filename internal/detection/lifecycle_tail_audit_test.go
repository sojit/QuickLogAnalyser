package detection
import("os";"path/filepath";"strings";"testing")
func TestLifecycleAuditTailKeepsOriginalLineNumber(t *testing.T){
 const ordinary=800000
 path:=filepath.Join(t.TempDir(),"large.log")
 text:=strings.Repeat("INFO ordinary historical entry\n",ordinary)+"2026-10-23 11:38:00 ERROR exact-tail-marker\n"
 if err:=os.WriteFile(path,[]byte(text),0600);err!=nil{t.Fatal(err)}
 f,err:=os.Open(path);if err!=nil{t.Fatal(err)};defer f.Close()
 res,err:=Scan(f);if err!=nil{t.Fatal(err)}
 found:=false
 for _,entry:=range res.Findings {if strings.Contains(entry.Message,"exact-tail-marker"){
  found=true
  if entry.LineStart!=ordinary+1 {t.Errorf("tail reference wrong: got line %d want %d; partial=%v",entry.LineStart,ordinary+1,res.Partial)}
 }}
 if !found{t.Fatal("tail marker missing")}
}
