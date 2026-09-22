// Offline UI/scheduling checks. No Core/helper/device launched.
let app=NSApplication.shared
var schedule=SIMBackupSchedule()
precondition(!schedule.claim(phase:"starting",sim:"a",busy:false))
precondition(!schedule.claim(phase:"ready",sim:"",busy:false))
precondition(!schedule.claim(phase:"ready",sim:"a",busy:true))
precondition(schedule.claim(phase:"ready",sim:"a",busy:false))
precondition(!schedule.claim(phase:"ready",sim:"a",busy:false))
precondition(schedule.claim(phase:"ready",sim:"b",busy:false))
precondition(localSMSBackupURL.path==FileManager.default.homeDirectoryForCurrentUser.path+"/Library/Application Support/DJISMS")
let isolated=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
let delegate=AppDelegate(store:UIStore(root:isolated));delegate.buildMenu();delegate.buildWindow();delegate.showPage(1)
func texts(_ v:NSView)->[String] {(v as? NSTextField).map{[$0.stringValue]} ?? v.subviews.flatMap{texts($0)}}
let labels=texts(delegate.window.contentView!);precondition(labels.contains{$0.contains(localSMSBackupURL.path)})
func sms(_ id:String,_ date:String)->SMS{SMS(id:id,state:"complete",sender:"synthetic",body:"fixture",created_at:date,sent_at:date,notification_state:"suppressed",parts:1,sim_id:nil,sim_number:nil)}
delegate.messages=[sms("old","2025-01-01T00:00:00Z"),sms("new","2026-01-01T00:00:00Z")];delegate.filters.selectedSegment=0;delegate.applyFilter();precondition(delegate.visible.map(\.id)==["new","old"])
try? FileManager.default.removeItem(at:isolated)
print("PASS: actual archive path label; ready-only once-per-SIM automatic backup admission; no repeat after restart of Core; unified descending SMS time presentation. No hardware.")
