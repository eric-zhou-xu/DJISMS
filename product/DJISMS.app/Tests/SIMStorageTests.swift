// Isolated new-feature UI tests: no Core/helper or device is launched.
let app=NSApplication.shared
app.setActivationPolicy(.accessory)
precondition(storageText(["used":40,"total":50]).contains("接近满"))
precondition(storageText(["used":50,"total":50]).contains("已满"))
precondition(storageText(nil).contains("设备未提供"))
let panel=SIMStoragePanel()
let records:[[String:Any]]=(1...2).map{["index":$0,"status":0,"sender":"+8613800138000","time":"2026-01-01T00:00:00Z","body":"Synthetic UI fixture \($0)","receipt":"fixture-\($0)","pdu_sha256":"fixture-hash-\($0)","archived":true]}
let sample:[String:Any]=["items":records,"me":["used":0,"total":23],"sm":["used":2,"total":50],"observed":"offline synthetic"]
panel.load(sample);precondition(!panel.delete.isEnabled)
var actions:[String]=[]
panel.perform={req,done in actions.append(req["action"] as! String);if req["action"] as? String=="plan"{precondition((req["indices"] as! [Int])==[1,2]);done(["items":records,"plan":"synthetic-plan","restored":true])}else{precondition(req["confirmed"] as? Bool==true);done(["items":[],"restored":true])}}
panel.table.selectRowIndexes(IndexSet([0,1]),byExtendingSelection:false)
Timer.scheduledTimer(withTimeInterval:0.05,repeats:false){_ in app.stopModal(withCode:.alertFirstButtonReturn)}
panel.prepareDelete();precondition(actions==["plan"],"cancel executed delete")
panel.load(sample);panel.table.selectRowIndexes(IndexSet([0,1]),byExtendingSelection:false)
Timer.scheduledTimer(withTimeInterval:0.05,repeats:false){_ in app.stopModal(withCode:.alertSecondButtonReturn)}
panel.prepareDelete();precondition(actions==["plan","plan","delete"])
var unsafe=records;unsafe[0]["archived"]=false;panel.load(["items":unsafe]);panel.table.selectRowIndexes(IndexSet(integer:0),byExtendingSelection:false);panel.prepareDelete();precondition(actions.count==3,"unarchived item reached helper")
print("PASS: dynamic storage warnings; empty selection disabled; multi-select dry run; cancel sends no delete; explicit confirmation; archive uncertainty blocks before helper. Offline fixtures only.")
