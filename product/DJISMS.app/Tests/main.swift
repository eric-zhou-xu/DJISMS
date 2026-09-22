import AppKit
let root=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
let s=UIStore(root:root)
func message(_ id:String,_ sim:String?,_ number:String?)->SMS {SMS(id:id,state:"complete",sender:"10010",body:"中文长文本测试",created_at:"2026-09-22T00:00:00Z",sent_at:nil,notification_state:"pending",parts:1,sim_id:sim,sim_number:number)}
let old=message("old",nil,nil),a=message("a","cardA","+8613800000000"),b=message("b","cardB","+8613900000000")
s.observe([old,a,b]);assert(s.stamp(old)=="接收 SIM 未记录");assert(s.stamp(a)=="+8613800000000");assert(s.stamp(b)=="+8613900000000")
s.data.numbers["cardA"]="+8613700000000";s.save();assert(s.stamp(a)=="+8613800000000","display override rewrote history")
let next=message("next","cardA","+8613800000000");s.observe([next]);assert(s.stamp(next)=="+8613700000000")
s.data.read.insert(a.id);s.save();let again=UIStore(root:root);assert(again.data.read.contains(a.id));assert(again.stamp(a)=="+8613800000000");assert(again.stamp(old)=="接收 SIM 未记录");assert(again.error==nil)
assert(parseDate("2026-09-22T16:00:00+08:00") != nil);assert(parseDate("2026-09-22T08:00:00.123Z") != nil)
try FileManager.default.removeItem(at:root)
print("PASS: per-SIM labels, immutable historical number stamps, legacy unknown, unread persistence, ISO dates")
