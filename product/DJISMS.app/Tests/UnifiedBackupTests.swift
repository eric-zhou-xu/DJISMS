// Offline UI/scheduling checks. No Core/helper/device launched.
let app=NSApplication.shared
precondition(localSMSBackupURL.path==FileManager.default.homeDirectoryForCurrentUser.path+"/Library/Application Support/DJISMS")
let isolated=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
let delegate=AppDelegate(store:UIStore(root:isolated));delegate.buildMenu();delegate.buildWindow();delegate.showPage(1)
func texts(_ v:NSView)->[String] {(v as? NSTextField).map{[$0.stringValue]} ?? v.subviews.flatMap{texts($0)}}
let labels=texts(delegate.window.contentView!);precondition(labels.contains{$0.contains(localSMSBackupURL.path)})
func sms(_ id:String,_ date:String)->SMS{SMS(id:id,state:"complete",sender:"synthetic",body:"fixture",created_at:date,sent_at:date,notification_state:"suppressed",parts:1,sim_id:nil,sim_number:nil)}
delegate.messages=[sms("old","2025-01-01T00:00:00Z"),sms("new","2026-01-01T00:00:00Z")];delegate.filters.selectedSegment=0;delegate.applyFilter();precondition(delegate.visible.map(\.id)==["new","old"])
var launches=0
 delegate.makeCoreClient={launches+=1;return CoreClient(executable:URL(fileURLWithPath:"/usr/bin/true"))}
 delegate.runSIM={_,done in done(["error":"fixture unavailable","restored":false])}
 delegate.performSIM(["action":"inspect"]){_ in}
 precondition(launches==1 && delegate.simFeatureFailure=="fixture unavailable","optional failure blocked Core launch")
 delegate.timer?.invalidate()
 try? FileManager.default.removeItem(at:isolated)
print("PASS: actual archive path label; explicit SIM backup isolated from Core startup; unified descending SMS time presentation. No hardware.")
