import Foundation
let root=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
try FileManager.default.createDirectory(at:root,withIntermediateDirectories:true)
let fatal=root.appendingPathComponent("fatal.py"), ready=root.appendingPathComponent("ready.py")
try "#!/usr/bin/python3\nimport sys\nsys.stdout.write('{\"version\":1,\"event\":\"fatal\",\"error\":\"archive is already owned by another DJISMS process\"}\\n');sys.stdout.flush();sys.exit(1)\n".write(to:fatal,atomically:true,encoding:.utf8)
try "#!/usr/bin/python3\nimport sys,json\nprint(json.dumps({'version':1,'event':'hello'}),flush=True)\nr=json.loads(sys.stdin.readline());print(json.dumps({'version':1,'id':r['id'],'data':{'state':{'phase':'ready'}}}),flush=True)\n".write(to:ready,atomically:true,encoding:.utf8)
for p in [fatal,ready]{try FileManager.default.setAttributes([.posixPermissions:0o700],ofItemAtPath:p.path)}
var active:CoreClient?;var round=0;var frames:[String]=[]
func runFatal(){
 let c=CoreClient(executable:fatal);active=c;frames=[]
 c.event={e in frames.append(e["event"] as? String ?? "")}
 c.exited={
  assert(frames==["fatal"]);assert(c.lastFailure=="archive is already owned by another DJISMS process");assert(!c.isReady && !c.isRunning)
  round+=1;fputs("fatal run \(round) complete\n",stderr)
  if round<20{runFatal()}else{runReady()}
 }
 try! c.launch()
}
func runReady(){
 let c=CoreClient(executable:ready);active=c;var response=false
 c.event={e in if e["event"] as? String=="hello"{assert(c.isReady);c.request("status"){r in response=r["error"]==nil}}}
 c.exited={assert(response);assert(c.lastFailure==nil);assert(!c.isReady);try! FileManager.default.removeItem(at:root);print("PASS: 20 immediate fatal/EOF ordering runs; error retention; fresh client restart; hello/status/exit; no USB or archive access");exit(0)}
 try! c.launch()
}
DispatchQueue.main.asyncAfter(deadline:.now()+60){fputs("FAIL: startup test timeout\n",stderr);exit(1)}
runFatal();dispatchMain()
