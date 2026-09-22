// AppKit layout harness. Compiled with AppDelegate declaration, without production entrypoint.
let app=NSApplication.shared
app.setActivationPolicy(.accessory)
let out=URL(fileURLWithPath:CommandLine.arguments[1],isDirectory:true)
try! FileManager.default.createDirectory(at:out,withIntermediateDirectories:true)
let testStore=UIStore(root:out.appendingPathComponent("isolated-presentation"))
let delegate=AppDelegate(store:testStore)
delegate.buildMenu();delegate.buildWindow()
delegate.state=["connected":true,"phase":"ready","sim_id":"fixture-sim-a","sim_number":"+86 138 1234 5678","sim":"+CPIN: READY","operator":"+COPS: 0,0,\"CHN-UNICOM\",7","lte":"+CEREG: 0,1","signal":"+CSQ: 25,99","used":12,"capacity":250,"ipv4":"192.168.225.28"]
let body=String(repeating:"中文长短信用于核对小窗口换行、完整正文滚动与固定信息区布局。",count:30)+"完整结束"
let sms=SMS(id:"layout-fixture",state:"complete",sender:"10683533599336700989",body:body,created_at:"2026-09-22T09:00:00Z",sent_at:"2026-09-22T08:59:59Z",notification_state:"submitted",parts:5,sim_id:"fixture-sim-a",sim_number:"+86 138 1234 5678")
delegate.messages=[sms];testStore.observe([sms])
func shot(_ w:NSWindow,_ name:String){
 w.orderFront(nil);app.updateWindows();RunLoop.current.run(until:Date().addingTimeInterval(0.1));let v=w.contentView!;v.layoutSubtreeIfNeeded();v.displayIfNeeded()
 let bitmap=v.bitmapImageRepForCachingDisplay(in:v.bounds)!;v.cacheDisplay(in:v.bounds,to:bitmap)
 try! bitmap.representation(using:.png,properties:[:])!.write(to:out.appendingPathComponent(name+".png"))
 print("PASS render \(name) \(Int(v.bounds.width))x\(Int(v.bounds.height))")
}
for mode in ["light","dark"] {
 app.appearance=NSAppearance(named:mode=="light" ? .aqua:.darkAqua)
 for size in [NSSize(width:620,height:440),NSSize(width:760,height:592),NSSize(width:1040,height:760)] {
  delegate.window.setFrame(NSRect(origin:.zero,size:size),display:false)
  for page in 0...3 {delegate.showPage(page);shot(delegate.window,"\(mode)-\(Int(size.width))-page\(page)")}
 }
 delegate.showDetail(sms)
 for size in [NSSize(width:390,height:460),NSSize(width:438,height:622),NSSize(width:680,height:800)] {
  delegate.detailWindow!.setFrame(NSRect(origin:.zero,size:size),display:false)
  shot(delegate.detailWindow!,"\(mode)-\(Int(size.width))-detail");precondition(abs(delegate.detailWindow!.frame.width-size.width)<1 && abs(delegate.detailWindow!.frame.height-size.height)<1,"Detail layout grew past requested size")
  let texts=delegate.detailWindow!.contentView!.subviews.flatMap{[$0]+$0.subviews}.compactMap{$0 as? NSScrollView}
  precondition(!texts.isEmpty)
 }
 delegate.detailWindow!.close()
}
print("PASS isolated AppKit layout matrix: 30 renders; no core process launched")
