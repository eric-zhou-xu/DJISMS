import AppKit

var localSMSBackupURL:URL { FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("个人程序/DJISMS/DJISMS 短信",isDirectory:true) }

struct SIMRecord {
    let index:Int, status:Int
    let sender:String, time:String, body:String, receipt:String, hash:String
    let archived:Bool
    init?(_ v:[String:Any]){guard let i=v["index"] as? Int else{return nil};index=i;status=v["status"] as? Int ?? -1;sender=v["sender"] as? String ?? "";time=v["time"] as? String ?? "";body=v["body"] as? String ?? "";receipt=v["receipt"] as? String ?? "";hash=v["pdu_sha256"] as? String ?? "";archived=v["archived"] as? Bool ?? false}
    var state:String {[0:"未读",1:"已读",2:"未发送",3:"已发送"][status] ?? "设备未提供"}
    var summary:String {"SM #\(index) · \(sender.isEmpty ? "未知号码":sender) · \(time.isEmpty ? "时间未提供":time)\n\(body.isEmpty ? "非文本或未完整解码":String(body.prefix(120)))\n\(state) · \(archived ? "Mac 原始归档已核验":"归档/身份不确定，禁止删除")"}
}
func storageText(_ v:[String:Any]?) -> String {
    guard let v=v,let used=v["used"] as? Int,let total=v["total"] as? Int,total>0,used>=0,used<=total else{return "设备未提供 / 尚未读取"}
    return "\(used) / \(total) 条" + (used==total ? " · 已满，请查看并自行选择处理":used*100>=total*80 ? " · 接近满容量":"")
}
final class SIMHelper {
    static func run(_ request:[String:Any],completion:@escaping([String:Any])->Void){
        let p=Process(),input=Pipe(),output=Pipe();p.executableURL=Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/djisms-sim-store");p.standardInput=input;p.standardOutput=output;p.standardError=FileHandle.nullDevice;p.environment=["HOME":FileManager.default.homeDirectoryForCurrentUser.path,"PATH":"/usr/bin:/bin:/usr/sbin:/sbin","LANG":"en_US.UTF-8"]
        do{try p.run();var bytes=try JSONSerialization.data(withJSONObject:request);bytes.append(10);try input.fileHandleForWriting.write(contentsOf:bytes);try input.fileHandleForWriting.close()}catch{completion(["error":error.localizedDescription,"restored":false]);return}
        DispatchQueue.global(qos:.userInitiated).async {
            let bytes=output.fileHandleForReading.readDataToEndOfFile();p.waitUntilExit()
            let result = bytes.count<=8_000_000 ? (try? JSONSerialization.jsonObject(with:bytes) as? [String:Any]):nil
            DispatchQueue.main.async {completion(result ?? ["error":"SIM 管理返回不完整，保持停止。","restored":false])}
        }
    }
}
final class SIMStoragePanel:NSWindowController,NSTableViewDataSource,NSTableViewDelegate {
    let table=NSTableView(),status=NSTextField(wrappingLabelWithString:"读取 SM 前会安全暂停接收；不会自动删除 SIM 短信。"),read=NSButton(),delete=NSButton()
    var items:[SIMRecord]=[]
    var perform:(([String:Any],@escaping([String:Any])->Void)->Void)?
    init(){
        let w=NSWindow(contentRect:NSRect(x:0,y:0,width:820,height:540),styleMask:[.titled,.closable,.resizable],backing:.buffered,defer:false);super.init(window:w);w.title="SIM 短信管理（SM）";w.isReleasedWhenClosed=false;w.minSize=NSSize(width:700,height:430)
        let root=NSView();w.contentView=root
        read.title="备份 SIM 短信到本地";read.target=self;read.action=#selector(refresh);read.bezelStyle = .rounded
        delete.title="删除所选 SIM 短信…";delete.target=self;delete.action=#selector(prepareDelete);delete.bezelStyle = .rounded;delete.isEnabled=false
        let buttons=stack([read,delete],vertical:false,spacing:12);let scroll=NSScrollView();scroll.hasVerticalScroller=true;scroll.documentView=table;table.delegate=self;table.dataSource=self;table.allowsMultipleSelection=true;table.rowHeight=96;table.target=self;table.doubleAction=#selector(showRecord);table.headerView=nil;table.columnAutoresizingStyle = .uniformColumnAutoresizingStyle;table.addTableColumn(NSTableColumn(identifier:NSUserInterfaceItemIdentifier("sim")));table.setAccessibilityLabel("SIM 实际短信列表，支持多选")
        let foot=NSTextField(wrappingLabelWithString:"读取后自动备份到与模块短信相同的本地短信列表，按时间查看。此处仅显示 SIM 当前记录。按住 Command/Shift 多选。删除前会生成预览并再次要求确认；仅精确删除所选 index，不影响 Mac 历史原件。")
        let v=stack([status,buttons,scroll,foot],spacing:12);pin(v,root,18);scroll.heightAnchor.constraint(greaterThanOrEqualToConstant:260).isActive=true;scroll.widthAnchor.constraint(equalTo:v.widthAnchor).isActive=true;status.widthAnchor.constraint(equalTo:v.widthAnchor).isActive=true;foot.widthAnchor.constraint(equalTo:v.widthAnchor).isActive=true
    }
    required init?(coder:NSCoder){fatalError()}
    func busy(_ value:Bool){read.isEnabled = !value;delete.isEnabled = !value && !table.selectedRowIndexes.isEmpty;table.isEnabled = !value}
    func load(_ v:[String:Any]){items=(v["items"] as? [[String:Any]] ?? []).compactMap(SIMRecord.init);table.reloadData();delete.isEnabled=false;status.stringValue="4G 模块（ME）\(storageText(v["me"] as? [String:Any]))  ·  SIM（SM）\(storageText(v["sm"] as? [String:Any]))\n查询时间 \(v["observed"] as? String ?? "未知")";if let e=v["error"] as? String,!e.isEmpty{status.stringValue=e}}
    @objc func refresh(){busy(true);status.stringValue="正在读取 SM、保存原始证据并恢复 ME 配置…";perform?(["action":"inspect"]){[weak self] v in self?.busy(false);self?.load(v)}}
    @objc func prepareDelete(){let selected=table.selectedRowIndexes.compactMap{items.indices.contains($0) ? items[$0]:nil};guard !selected.isEmpty else{return};guard selected.allSatisfy({$0.archived && !$0.receipt.isEmpty})else{status.stringValue="所选记录含归档/身份不确定项，禁止删除。";return};busy(true);status.stringValue="正在生成删除 Dry Run，尚未执行删除…";perform?(["action":"plan","indices":selected.map(\.index)]){[weak self] v in
        guard let self=self else{return};self.busy(false)
        guard let token=v["plan"] as? String,v["restored"] as? Bool==true,(v["error"] as? String ?? "").isEmpty else{self.status.stringValue=v["error"] as? String ?? "无法建立可靠删除计划";return}
        let targets=(v["items"] as? [[String:Any]] ?? []).compactMap(SIMRecord.init);guard !targets.isEmpty,targets.allSatisfy({$0.archived})else{self.status.stringValue="删除计划缺少归档证明";return}
        let alert=NSAlert();alert.alertStyle = .warning;alert.messageText="删除所选 \(targets.count) 条 SIM 短信？";alert.informativeText="身份不确定项：无（本次 SIM + index + 精确 PDU 核验）。Mac 归档保留；仅删除预览中的 SM 记录，操作不可撤销。计划有效期两分钟。"
        let preview=NSTextView(frame:NSRect(x:0,y:0,width:550,height:300));preview.isEditable=false;preview.isVerticallyResizable=true;preview.maxSize=NSSize(width:550,height:CGFloat.greatestFiniteMagnitude);preview.textContainer?.widthTracksTextView=true;preview.string=targets.map(\.summary).joined(separator:"\n\n");preview.font = .systemFont(ofSize:13);let previewScroll=NSScrollView(frame:NSRect(x:0,y:0,width:550,height:300));previewScroll.hasVerticalScroller=true;previewScroll.documentView=preview;alert.accessoryView=previewScroll;alert.addButton(withTitle:"取消");alert.addButton(withTitle:"确认删除所选 SIM 短信")
        guard alert.runModal() == .alertSecondButtonReturn else{self.status.stringValue="已取消；未执行删除。";return}
        self.busy(true);self.status.stringValue="正在逐条删除并核验完整 SM 库存…";self.perform?(["action":"delete","plan":token,"confirmed":true]){[weak self] result in self?.busy(false);self?.load(result)}
    }}
    @objc func showRecord(){guard items.indices.contains(table.selectedRow)else{return};let r=items[table.selectedRow];let a=NSAlert();a.messageText=r.sender.isEmpty ? "未知发送方":r.sender;a.informativeText="SM #\(r.index) · \(r.state) · \(r.time)\n\n\(r.body.isEmpty ? "设备 PDU 尚无法完整解码为文本":r.body)\n\nMac 归档：\(r.archived ? "已核验":"不确定，禁止删除")\nPDU SHA-256：\(r.hash)";a.addButton(withTitle:"关闭");a.runModal()}
    func numberOfRows(in tableView:NSTableView)->Int{items.count}
    func tableView(_ tableView:NSTableView,viewFor tableColumn:NSTableColumn?,row:Int)->NSView?{guard items.indices.contains(row)else{return nil};let item=items[row];let text=NSTextField(wrappingLabelWithString:item.summary);text.font = .systemFont(ofSize:13);let attributed=NSMutableAttributedString(string:item.summary);let length=(item.summary as NSString).range(of:"\n").location;if length != NSNotFound{attributed.addAttribute(.font,value:NSFont.systemFont(ofSize:14,weight:.semibold),range:NSRange(location:0,length:length))};text.attributedStringValue=attributed;text.setAccessibilityLabel(item.summary);return text}
    func tableViewSelectionDidChange(_ notification:Notification){delete.isEnabled=read.isEnabled && !table.selectedRowIndexes.isEmpty}
}
extension AppDelegate {
    @objc func showSIMStorage(){if simPanel==nil{let p=SIMStoragePanel();p.perform={ [weak self] request,done in self?.performSIM(request,completion:done)};simPanel=p};simPanel?.showWindow(nil);simPanel?.window?.makeKeyAndOrderFront(nil)}
    func performSIM(_ request:[String:Any],completion:@escaping([String:Any])->Void){
        guard !storageBusy,!shuttingDown else{completion(["error":"接收/存储操作正在结束，请稍后重试。"]);return};storageBusy=true;timer?.invalidate()
        let launch={ [weak self] in guard let self=self else{return};self.runSIM(request){[weak self] value in guard let self=self else{return};self.storageBusy=false;self.simFeatureFailure=value["error"] as? String
                if value["restored"] as? Bool==true,value["sm"] as? [String:Any] != nil{self.simSnapshot=value}
                // Core independently checks durable integrity/selection hazards. Optional helper availability never gates Core startup.
                self.startReceiver();completion(value)}}
        if client.isRunning{pendingStorageLaunch=launch;client.request("shutdown")}else{launch()}
    }
}
