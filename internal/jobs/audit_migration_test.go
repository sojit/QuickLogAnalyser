package jobs
import("path/filepath";"testing";"quickanalyser/internal/store")
func TestAuditRefreshPreservesFindingsWhenEvidenceMissing(t *testing.T){
 dir:=t.TempDir();st,err:=store.Open(filepath.Join(dir,"results.sqlite"));if err!=nil{t.Fatal(err)};defer st.Close()
 statements:=[]string{
 `INSERT INTO jobs(id,original_name,state,stage,created_utc,updated_utc) VALUES('job','sample.tar','completed','done','2026-10-23','2026-10-23')`,
 `INSERT INTO sources(id,job_id,raw_path,kind,cache_path,coverage,source_kind,classify_version) VALUES('src','job','var/log/app.log','regular','evidence/missing.txt','complete','log','1.0.0')`,
 `INSERT INTO events(id,job_id,source_id,severity,message,line_start,line_end,fingerprint) VALUES('event','job','src','error','existing useful finding',1,1,'fp')`,
 }
 for _,sql:=range statements {if _,err:=st.DB.Exec(sql);err!=nil{t.Fatal(err)}}
 result,err:=EnsureJobCompatibility(st,"job",dir)
 var n int;if e:=st.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id='job'`).Scan(&n);e!=nil{t.Fatal(e)}
 if n!=1{t.Fatalf("refresh lost old findings: remaining=%d err=%v findings_rebuilt=%v missing=%v",n,err,result.FindingsRebuilt,result.Missing)}
}
