package server
import("path/filepath";"strings";"testing";"quickanalyser/internal/storage")
// Resolve only: never invokes deletion or touches anything outside the test directory.
func TestLifecycleAuditDeleteTargetMustStayUnderJobs(t *testing.T){
 m,err:=storage.OpenTestRoot(t.TempDir());if err!=nil{t.Fatal(err)};defer m.Close()
 for _,id:=range []string{"..",`..\..`,`..\victim`}{
  target:=m.JobDir(id);relative,err:=filepath.Rel(m.JobsDir(),target)
  if err!=nil||relative==".."||strings.HasPrefix(relative,".."+string(filepath.Separator)){t.Errorf("unvalidated job id %q escapes jobs directory: %q",id,relative)}
 }
}
