import AppKit
import UserNotifications
import ServiceManagement

final class AppDelegate: NSObject, NSApplicationDelegate, NSTableViewDataSource, NSTableViewDelegate, NSSearchFieldDelegate, UNUserNotificationCenterDelegate {
    var client=CoreClient()
    var makeCoreClient:()->CoreClient = {CoreClient()}
    var runSIM:([String:Any],@escaping([String:Any])->Void)->Void = {SIMHelper.run($0,completion:$1)}
    var storageBusy=false,storageChecked=false
    var simFeatureFailure:String?
    var pendingStorageLaunch:(()->Void)?
    var simSnapshot:[String:Any]?
    var simPanel:SIMStoragePanel?
    let store:UIStore
    init(store:UIStore=UIStore()){self.store=store;super.init()}
    var window:NSWindow!, item:NSStatusItem!, sidebar:NSVisualEffectView!, content=NSView()
    var nav:[NavButton]=[], page=0, messages:[SMS]=[], visible:[SMS]=[]
    let search=NSSearchField(), table=NSTableView(), filters=NSSegmentedControl(labels:["全部","未读","今天","7天","30天"],trackingMode:.selectOne,target:nil,action:nil)
    let count=label("正在读取永久短信…",11,.regular,.secondaryLabelColor), footerNumber=label("本机号码未读取",11,.medium,.secondaryLabelColor)
    let statusText=label("正在校验永久档案…",12,.regular,.secondaryLabelColor)
    var fields:[String:NSTextField]=[:], deviceDot:NSImageView?, modifyButton:NSButton?
    var state:[String:Any]=[:], notifications=true, autoPurge=true, permissionKnown=false, permissionGranted=false, sound=true
    var notificationButton:NSButton?, purgeButton:NSButton?, soundButton:NSButton?, loginButton:NSButton?, permissionLabel:NSTextField?
    var timer:Timer?, menuHeader=NSView(), menuTitle=label("正在启动",13,.semibold), menuNumber=label("本机号码待读取",13), menuNetwork=label("连接后自动接收",11,.regular,.secondaryLabelColor), menuDot=icon("circle.fill",size:10,color:.systemOrange), recentItem:NSMenuItem!
    var moreButton:NSButton?
    var loading=false, more=false, generation=0, historyScheduled=false, notificationInFlight=Set<String>(), shuttingDown=false, polling=false
    var detailWindow:NSWindow?, currentMessage:SMS?, detailMark:NSButton?, errorShown=false
    var build:String { Bundle.main.object(forInfoDictionaryKey:"DJISMSBuildID") as? String ?? "UI Candidate" }
    var simID:String { state["sim_id"] as? String ?? "" }
    var simNumber:String { guard !simID.isEmpty else{return ""}; return store.number(id:simID,reported:state["sim_number"] as? String ?? "") }
    var connected:Bool { state["connected"] as? Bool ?? false }
    var phase:String { state["phase"] as? String ?? "starting" }
    var receiving:Bool { phase == "ready" || phase == "archiving" }
    var phaseName:String { ["starting":"正在校验档案","checking":"正在连接","ready":"正在接收","archiving":"正在保存短信","disconnected":"未连接","reconnecting":"正在恢复连接","waiting_network":"等待移动网络","interface_busy":"设备正在被使用","sleeping":"等待系统唤醒","safety_stop":"接收已暂停","multiple_devices":"请只连接一个模块","unsupported":"暂不支持此设备","stopped":"接收已停止"][phase] ?? "正在连接" }
    var statusColor:NSColor { receiving ? .systemGreen : (phase == "disconnected" || phase == "safety_stop" || phase == "stopped" ? .systemRed:.systemOrange) }
    var carrier:String { guard connected else{return "待连接"}; let raw=state["operator"] as? String ?? ""; let parts=raw.split(separator:","); guard parts.count>2 else{return "待读取"};let name=parts[2].trimmingCharacters(in:CharacterSet(charactersIn:" \""));return ["CHN-UNICOM":"中国联通","CHINA MOBILE":"中国移动","CHN-CT":"中国电信","CHINA TELECOM":"中国电信" ][name.uppercased()] ?? name }
    var lte:String { guard connected else{return "待连接"};let a=(state["lte"] as? String ?? "").split(separator:",").last?.trimmingCharacters(in:.whitespaces) ?? "";return a == "1" || a == "5" ? "LTE":"待注册" }
    var signal:String { guard connected else{return "未知"};let t=(state["signal"] as? String ?? "").replacingOccurrences(of:"+CSQ:",with:"").split(separator:",").first?.trimmingCharacters(in:.whitespaces) ?? "";let v=Int(t) ?? 99;return v == 99 ? "未知":v>=20 ? "强":v>=10 ? "良好":"弱" }
    func applicationDidFinishLaunching(_ notification:Notification) {
        NSApp.setActivationPolicy(.accessory); UNUserNotificationCenter.current().delegate=self
        sound=store.data.sound; applyAppearance(); buildMenu(); buildWindow(); showPage(0); showWindow()
        NSWorkspace.shared.notificationCenter.addObserver(self,selector:#selector(sleeping),name:NSWorkspace.willSleepNotification,object:nil)
        NSWorkspace.shared.notificationCenter.addObserver(self,selector:#selector(waking),name:NSWorkspace.didWakeNotification,object:nil)
        startReceiver()
        checkPermission(request:true)
    }
    func startReceiver(){
        guard !shuttingDown,!client.isRunning,!storageBusy else{return}
        timer?.invalidate();polling=false
        client=makeCoreClient()
        client.event={ [weak self] in self?.handle($0) }
        client.exited={ [weak self] in
            guard let self=self else{return}
            self.timer?.invalidate();self.polling=false
            if let launch=self.pendingStorageLaunch{self.pendingStorageLaunch=nil;launch();return}
            self.unavailable(self.client.lastFailure ?? "核心服务已停止。点击刷新状态重新启动并核验档案。",phase:"stopped")
            if self.shuttingDown{NSApp.reply(toApplicationShouldTerminate:true)}
        }
        unavailable("正在启动接收服务并校验永久档案…",phase:"starting")
        do{try client.launch()}catch{unavailable("无法启动接收服务：\(error.localizedDescription)",phase:"stopped")}
        timer=Timer.scheduledTimer(withTimeInterval:2,repeats:true){[weak self] _ in self?.poll()}
    }
    func button(_ title:String,_ action:Selector) -> NSButton { let b=NSButton(title:title,target:self,action:action);b.bezelStyle = .rounded;return b }
    func buildMenu() {
        let main=NSMenu();let app=NSMenuItem();let appMenu=NSMenu()
        appMenu.addItem(withTitle:"关于 DJISMS",action:#selector(about),keyEquivalent:"");appMenu.addItem(.separator());appMenu.addItem(withTitle:"设置…",action:#selector(settings),keyEquivalent:",");appMenu.addItem(.separator());appMenu.addItem(withTitle:"退出 DJISMS",action:#selector(quit),keyEquivalent:"q");app.submenu=appMenu;main.addItem(app)
        let edit=NSMenuItem(title:"编辑",action:nil,keyEquivalent:"");let em=NSMenu();em.addItem(withTitle:"复制",action:#selector(NSText.copy(_:)),keyEquivalent:"c");em.addItem(withTitle:"粘贴",action:#selector(NSText.paste(_:)),keyEquivalent:"v");em.addItem(withTitle:"全选",action:#selector(NSText.selectAll(_:)),keyEquivalent:"a");edit.submenu=em;main.addItem(edit)
        let view=NSMenuItem(title:"显示",action:nil,keyEquivalent:"");let vm=NSMenu();vm.addItem(withTitle:"打开主窗口",action:#selector(showWindow),keyEquivalent:"0");vm.addItem(withTitle:"查看所选短信",action:#selector(openSelected),keyEquivalent:"\r");view.submenu=vm;main.addItem(view);NSApp.mainMenu=main
        item=NSStatusBar.system.statusItem(withLength:NSStatusItem.variableLength);item.button?.image=symbol("antenna.radiowaves.left.and.right",16);item.button?.setAccessibilityLabel("DJISMS 接收状态")
        let menu=NSMenu();let header=NSMenuItem();menuHeader.frame=NSRect(x:0,y:0,width:288,height:85)
        let texts=stack([menuTitle,menuNumber,menuNetwork],spacing:4);texts.translatesAutoresizingMaskIntoConstraints=false;menuHeader.addSubview(texts);menuHeader.addSubview(menuDot)
        NSLayoutConstraint.activate([texts.leadingAnchor.constraint(equalTo:menuHeader.leadingAnchor,constant:43),texts.trailingAnchor.constraint(equalTo:menuHeader.trailingAnchor,constant:-12),texts.centerYAnchor.constraint(equalTo:menuHeader.centerYAnchor),menuDot.leadingAnchor.constraint(equalTo:menuHeader.leadingAnchor,constant:18),menuDot.topAnchor.constraint(equalTo:menuHeader.topAnchor,constant:18)])
        header.view=menuHeader;menu.addItem(header);menu.addItem(.separator())
        func add(_ text:String,_ iconName:String,_ action:Selector)->NSMenuItem{let m=NSMenuItem(title:text,action:action,keyEquivalent:"");m.target=self;m.image=symbol(iconName,15);menu.addItem(m);return m}
        _=add("打开主窗口","macwindow",#selector(showWindow));recentItem=add("查看最新短信","bubble.left",#selector(latest));_=add("设备状态","antenna.radiowaves.left.and.right",#selector(device));_=add("设置…","gearshape",#selector(settings));_=add("帮助","questionmark.circle",#selector(help));menu.addItem(.separator());_=add("退出 DJISMS","power",#selector(quit));item.menu=menu
    }
    func buildWindow(){
        window=NSWindow(contentRect:NSRect(x:0,y:0,width:760,height:560),styleMask:[.titled,.closable,.miniaturizable,.resizable],backing:.buffered,defer:false)
        window.title="DJISMS";window.minSize=NSSize(width:620,height:440);window.isReleasedWhenClosed=false;window.center();window.tabbingMode = .disallowed
        let root=NSView();window.contentView=root
        sidebar=NSVisualEffectView();sidebar.material = .sidebar;sidebar.blendingMode = .withinWindow;sidebar.state = .followsWindowActiveState;sidebar.translatesAutoresizingMaskIntoConstraints=false;root.addSubview(sidebar)
        content.translatesAutoresizingMaskIntoConstraints=false;root.addSubview(content)
        NSLayoutConstraint.activate([sidebar.leadingAnchor.constraint(equalTo:root.leadingAnchor),sidebar.topAnchor.constraint(equalTo:root.topAnchor),sidebar.bottomAnchor.constraint(equalTo:root.bottomAnchor),sidebar.widthAnchor.constraint(equalToConstant:174),content.leadingAnchor.constraint(equalTo:sidebar.trailingAnchor),content.trailingAnchor.constraint(equalTo:root.trailingAnchor),content.topAnchor.constraint(equalTo:root.topAnchor),content.bottomAnchor.constraint(equalTo:root.bottomAnchor)])
        for (i,v) in [("短信","bubble.left"),("设备状态","antenna.radiowaves.left.and.right"),("设置","gearshape"),("关于","info.circle")].enumerated(){let b=NavButton(title:"  "+v.0,target:self,action:#selector(navigate(_:)));b.tag=i;b.isBordered=false;b.alignment = .left;b.image=symbol(v.1,18);b.imagePosition = .imageLeading;b.font = .systemFont(ofSize:14,weight:.medium);b.setAccessibilityLabel(v.0);b.translatesAutoresizingMaskIntoConstraints=false;sidebar.addSubview(b);NSLayoutConstraint.activate([b.leadingAnchor.constraint(equalTo:sidebar.leadingAnchor,constant:12),b.trailingAnchor.constraint(equalTo:sidebar.trailingAnchor,constant:-12),b.topAnchor.constraint(equalTo:sidebar.topAnchor,constant:22+CGFloat(i)*46),b.heightAnchor.constraint(equalToConstant:40)]);nav.append(b)}
        let foot=stack([label("当前 SIM",10,.regular,.tertiaryLabelColor),footerNumber,label("短信永久保存在 Mac",10,.regular,.tertiaryLabelColor)],spacing:4);foot.translatesAutoresizingMaskIntoConstraints=false;sidebar.addSubview(foot);NSLayoutConstraint.activate([foot.leadingAnchor.constraint(equalTo:sidebar.leadingAnchor,constant:16),foot.trailingAnchor.constraint(equalTo:sidebar.trailingAnchor,constant:-10),foot.bottomAnchor.constraint(equalTo:sidebar.bottomAnchor,constant:-18),footerNumber.widthAnchor.constraint(equalTo:foot.widthAnchor)])
    }
    @objc func navigate(_ sender:NSButton){showPage(sender.tag)}
    func showPage(_ index:Int){page=index;nav.enumerated().forEach{$0.element.active=$0.offset==index};content.subviews.forEach{$0.removeFromSuperview()};fields.removeAll();deviceDot=nil;modifyButton=nil;notificationButton=nil;purgeButton=nil;soundButton=nil;permissionLabel=nil;loginButton=nil
        switch index{case 0:buildList();case 1:buildDevice();case 2:buildSettings();default:buildAbout()};renderStatus()
    }
    func buildList(){
        search.placeholderString="搜索短信、号码或内容…";search.delegate=self;search.sendsSearchStringImmediately=true;search.controlSize = .regular;search.setAccessibilityLabel("搜索发件号码或短信正文")
        filters.selectedSegment=max(0,filters.selectedSegment);filters.target=self;filters.action=#selector(filterChanged);filters.segmentStyle = .rounded;filters.segmentDistribution = .fillEqually
        for v in [search,filters,statusText]{v.translatesAutoresizingMaskIntoConstraints=false;content.addSubview(v)}
        let scroll=NSScrollView();scroll.hasVerticalScroller=true;scroll.borderType = .noBorder;scroll.drawsBackground=false;scroll.translatesAutoresizingMaskIntoConstraints=false;content.addSubview(scroll)
        if table.tableColumns.isEmpty {let col=NSTableColumn(identifier:NSUserInterfaceItemIdentifier("message"));table.addTableColumn(col)}
        table.headerView=nil;table.rowHeight=64;table.intercellSpacing=NSSize(width:0,height:1);table.style = .fullWidth;table.backgroundColor = .clear;table.selectionHighlightStyle = .regular;table.delegate=self;table.dataSource=self;table.target=self;table.doubleAction=#selector(openSelected);table.columnAutoresizingStyle = .uniformColumnAutoresizingStyle;table.setAccessibilityLabel("永久短信列表");scroll.documentView=table
        let moreControl=button("加载更多",#selector(loadMore));moreButton=moreControl;let bottom=stack([count,moreControl],vertical:false,spacing:8);bottom.translatesAutoresizingMaskIntoConstraints=false;content.addSubview(bottom);count.setContentCompressionResistancePriority(.defaultLow,for:.horizontal);count.setContentHuggingPriority(.defaultLow,for:.horizontal);bottom.views.last?.isHidden = !more
        NSLayoutConstraint.activate([search.leadingAnchor.constraint(equalTo:content.leadingAnchor,constant:14),search.trailingAnchor.constraint(equalTo:content.trailingAnchor,constant:-14),search.topAnchor.constraint(equalTo:content.topAnchor,constant:12),search.heightAnchor.constraint(equalToConstant:30),filters.leadingAnchor.constraint(equalTo:search.leadingAnchor),filters.trailingAnchor.constraint(equalTo:search.trailingAnchor),filters.topAnchor.constraint(equalTo:search.bottomAnchor,constant:9),filters.heightAnchor.constraint(equalToConstant:26),statusText.leadingAnchor.constraint(equalTo:search.leadingAnchor),statusText.trailingAnchor.constraint(equalTo:search.trailingAnchor),statusText.topAnchor.constraint(equalTo:filters.bottomAnchor,constant:6),statusText.heightAnchor.constraint(equalToConstant:18),scroll.leadingAnchor.constraint(equalTo:content.leadingAnchor,constant:8),scroll.trailingAnchor.constraint(equalTo:content.trailingAnchor,constant:-8),scroll.topAnchor.constraint(equalTo:statusText.bottomAnchor,constant:4),scroll.bottomAnchor.constraint(equalTo:bottom.topAnchor,constant:-6),bottom.leadingAnchor.constraint(equalTo:search.leadingAnchor),bottom.trailingAnchor.constraint(equalTo:search.trailingAnchor),bottom.bottomAnchor.constraint(equalTo:content.bottomAnchor,constant:-9),bottom.heightAnchor.constraint(equalToConstant:25)])
        applyFilter()
    }
    func scrollingCard()->Surface {
        let scroll=NSScrollView();scroll.hasVerticalScroller=true;scroll.drawsBackground=false;pin(scroll,content)
        let canvas=FlippedView();canvas.translatesAutoresizingMaskIntoConstraints=false;scroll.documentView=canvas
        canvas.widthAnchor.constraint(equalTo:scroll.contentView.widthAnchor).isActive=true
        let card=Surface();card.translatesAutoresizingMaskIntoConstraints=false;canvas.addSubview(card)
        NSLayoutConstraint.activate([card.leadingAnchor.constraint(equalTo:canvas.leadingAnchor,constant:18),card.trailingAnchor.constraint(equalTo:canvas.trailingAnchor,constant:-18),card.topAnchor.constraint(equalTo:canvas.topAnchor,constant:18),card.bottomAnchor.constraint(equalTo:canvas.bottomAnchor,constant:-18)])
        return card
    }
    func buildDevice(){
        let card=scrollingCard()
        let title=label("DJI 4G 模块",17,.semibold);let dot=icon("circle.fill",size:9,color:.systemGreen);deviceDot=dot;let desc=label("",12);fields["connection"]=desc
        let head=stack([icon("cellularbars",size:36),stack([title,stack([dot,desc],vertical:false,spacing:6)],spacing:7),button("刷新状态",#selector(refreshDevice))],vertical:false,spacing:14)
        let separator=NSBox();separator.boxType = .separator
        let rows=NSGridView();rows.rowSpacing=14;rows.columnSpacing=14;rows.xPlacement = .leading;rows.yPlacement = .center
        for (key,name) in [("number","本机号码"),("sim","SIM 状态"),("carrier","运营商"),("type","网络类型"),("signal","信号强度"),("registration","网络状态"),("cache","4G模块短信存储（ME）"),("smstorage","SIM短信存储（SM）"),("recovery","最近恢复"),("internet","Mac 4G"),("sample","状态更新")]{
            let value=label("待读取",key=="number" ? 16:13,key=="number" ? .semibold:.regular);fields[key]=value
            if key=="number"{let b=button("修改",#selector(editNumber));modifyButton=b;b.setContentHuggingPriority(.required,for:.horizontal);value.setContentHuggingPriority(.defaultLow,for:.horizontal);rows.addRow(with:[label(name,13,.regular,.secondaryLabelColor),stack([value,b],vertical:false,spacing:12)])}
            else{rows.addRow(with:[label(name,13,.regular,.secondaryLabelColor),value])}
            value.setContentCompressionResistancePriority(.defaultLow,for:.horizontal)
        }
        let foot=NSTextField(wrappingLabelWithString:"号码来自 SIM。修改只更新本机显示，不会更改 SIM。历史短信保留接收时的号码记录。");foot.font = .systemFont(ofSize:11);foot.textColor = .secondaryLabelColor
        rows.column(at:0).width = 150
        let backup=NSTextField(wrappingLabelWithString:"本地备份路径：\(localSMSBackupURL.path)\n模块与 SIM 短信统一保存在本地短信列表。通过 SIM 管理手动备份；不会自动删除 SIM 短信。");backup.font = .systemFont(ofSize:11);backup.textColor = .secondaryLabelColor;backup.isSelectable=true
        let v=stack([head,separator,rows,button("备份与管理 SIM 短信…",#selector(showSIMStorage)),foot,backup],spacing:17);pin(v,card,18);head.widthAnchor.constraint(equalTo:v.widthAnchor).isActive=true;head.views[1].setContentHuggingPriority(.defaultLow,for:.horizontal);rows.widthAnchor.constraint(equalTo:v.widthAnchor).isActive=true;rows.column(at:1).xPlacement = .fill;separator.widthAnchor.constraint(equalTo:v.widthAnchor).isActive=true;foot.widthAnchor.constraint(equalTo:v.widthAnchor).isActive=true;backup.widthAnchor.constraint(equalTo:v.widthAnchor).isActive=true
    }
    func checkbox(_ title:String,_ state:Bool,_ action:Selector)->NSButton{let b=NSButton(checkboxWithTitle:title,target:self,action:action);b.state=state ? .on:.off;b.font = .systemFont(ofSize:13);return b}
    func buildSettings(){
        let card=scrollingCard()
        let login=checkbox("登录时自动启动",SMAppService.mainApp.status == .enabled,#selector(loginChanged(_:)));loginButton=login
        let notification=checkbox("收到新短信时显示系统通知",notifications,#selector(preferencesChanged));notificationButton=notification
        let soundToggle=checkbox("播放提示音",sound,#selector(soundChanged(_:)));soundButton=soundToggle
        let permanent=checkbox("永久保存在这台 Mac（不可关闭）",true,#selector(noop));permanent.isEnabled=false
        let purge=checkbox("保存并核验后自动清理模块（ME）副本",autoPurge,#selector(preferencesChanged));purgeButton=purge
        let grid=NSGridView();grid.rowSpacing=16;grid.columnSpacing=20;grid.xPlacement = .leading;grid.yPlacement = .top
        grid.addRow(with:[label("启动",13,.medium),login]);grid.addRow(with:[label("通知",13,.medium),stack([notification,soundToggle],spacing:8)]);grid.addRow(with:[label("短信",13,.medium),stack([permanent,purge],spacing:8)])
        let appearance=NSPopUpButton();appearance.addItems(withTitles:["跟随系统","浅色","深色"]);appearance.selectItem(at:["system","light","dark"].firstIndex(of:store.data.appearance) ?? 0);appearance.target=self;appearance.action=#selector(appearanceChanged(_:));appearance.setAccessibilityLabel("外观");grid.addRow(with:[label("外观",13,.medium),appearance])
        grid.column(at:0).width = 44
        let permission=label("",11,.regular,.secondaryLabelColor);permissionLabel=permission
        let links=stack([permission,button("通知设置…",#selector(notificationSettings))],vertical:false,spacing:12)
        let note=NSTextField(wrappingLabelWithString:"所有短信完整保存在本地，不会上传到任何第三方服务。");note.font = .systemFont(ofSize:11);note.textColor = .secondaryLabelColor
        let v=stack([grid,links,note],spacing:20);pin(v,card,18);note.widthAnchor.constraint(equalTo:v.widthAnchor).isActive=true
    }
    func buildAbout(){
        let mark=NSImageView();mark.image=Bundle.main.url(forResource:"AppMark",withExtension:"png").flatMap{NSImage(contentsOf:$0)};mark.translatesAutoresizingMaskIntoConstraints=false;NSLayoutConstraint.activate([mark.widthAnchor.constraint(equalToConstant:64),mark.heightAnchor.constraint(equalToConstant:64)])
        let v=stack([mark,label("DJISMS",24,.bold),label("版本 1.0.0 · UI 候选",13),label("只接收短信 · 本地保存 · 简单可靠",13),label("© 2026 DJISMS. All rights reserved.",11,.regular,.secondaryLabelColor),label(build,10,.regular,.tertiaryLabelColor),button("使用帮助",#selector(help))],spacing:12);v.alignment = .centerX;v.translatesAutoresizingMaskIntoConstraints=false;content.addSubview(v);NSLayoutConstraint.activate([v.centerXAnchor.constraint(equalTo:content.centerXAnchor),v.centerYAnchor.constraint(equalTo:content.centerYAnchor),v.leadingAnchor.constraint(greaterThanOrEqualTo:content.leadingAnchor,constant:18),v.trailingAnchor.constraint(lessThanOrEqualTo:content.trailingAnchor,constant:-18)])
    }
    func poll(){guard client.isReady,!shuttingDown,!polling else{return};polling=true;client.request("status"){[weak self] response in guard let self=self else{return};self.polling=false;if response["error"] != nil{self.unavailable("接收状态暂时无法确认，正在等待服务恢复。");return};guard let data=response["data"] as? [String:Any] else{return};if let s=data["state"] as? [String:Any]{self.state=s};if let p=data["preferences"] as? [String:Any]{self.notifications=p["notifications"] as? Bool ?? true;self.autoPurge=p["auto_purge"] as? Bool ?? true};self.renderStatus();self.fetchNotifications()}}
    func handle(_ event:[String:Any]){if event["error"] != nil{unavailable(event["error"] as? String ?? "接收服务需要检查。",phase:"stopped");return};switch event["event"] as? String{case "initializing":unavailable("正在校验永久档案，完成后自动接收…",phase:"starting");case "hello":refreshHistory();poll();case "state":if let s=event["data"] as? [String:Any]{state=s;renderStatus()};case "history_changed":scheduleHistory();case "stopped":if shuttingDown{NSApp.reply(toApplicationShouldTerminate:true)};default:break}}
    func unavailable(_ text:String,phase:String="checking"){state=["phase":phase,"connected":false,"ui_failure":text];renderStatus();statusText.stringValue=text}
    func renderStatus(){
        menuTitle.stringValue="DJISMS \(phaseName)";menuNumber.stringValue=simNumber.isEmpty ? (connected ? "本机号码未提供":"本机号码待连接"):simNumber;menuNetwork.stringValue=connected ? "\(carrier) · \(lte) · 信号\(signal)":"连接后自动接收短信";menuDot.contentTintColor=statusColor;item.button?.toolTip="\(menuTitle.stringValue)\n\(menuNumber.stringValue)\n\(menuNetwork.stringValue)";item.button?.setAccessibilityLabel(item.button?.toolTip)
        footerNumber.stringValue=simNumber.isEmpty ? "本机号码未提供":simNumber;statusText.stringValue=state["ui_failure"] as? String ?? (phase=="safety_stop" ? "接收已暂停，短信档案保留。请查看使用帮助。":phaseName);statusText.textColor=phase=="safety_stop" ? .systemRed:.secondaryLabelColor
        deviceDot?.contentTintColor=statusColor;fields["connection"]?.stringValue=state["ui_failure"] as? String ?? (receiving ? "已连接，\(phaseName)":phaseName);fields["number"]?.stringValue=simNumber.isEmpty ? "未提供":simNumber;modifyButton?.isEnabled=connected && !simID.isEmpty
        fields["sim"]?.stringValue=connected ? ((state["sim"] as? String ?? "").contains("READY") ? "已插入":"等待就绪"):(phase=="stopped" ? "服务未运行，未读取":"待连接");fields["carrier"]?.stringValue=carrier;fields["type"]?.stringValue=lte == "LTE" ? "4G (LTE)":lte;fields["signal"]?.stringValue=signal;fields["signal"]?.textColor=signal=="强" ? .systemGreen:.labelColor;fields["registration"]?.stringValue=lte=="LTE" ? "已注册（4G）":(phase=="stopped" ? "服务未运行，未核验":"待注册")
        let used=state["used"] as? Int ?? 0,capacity=state["capacity"] as? Int ?? 0;fields["cache"]?.stringValue=connected && capacity>0 ? storageText(["used":used,"total":capacity]):"待读取"
        if let failure=simFeatureFailure {fields["smstorage"]?.stringValue="不可用 · \(failure)"}else if let snap=simSnapshot,snap["sim_id"] as? String==state["sim_id"] as? String {fields["smstorage"]?.stringValue=storageText(snap["sm"] as? [String:Any])+"（上次查询）"}else{fields["smstorage"]?.stringValue="尚未读取 · 点击 SIM 管理查询"}
        fields["recovery"]?.stringValue=(state["last_recovery"] as? String).map{dateText($0)} ?? "本次运行尚无恢复记录";fields["internet"]?.stringValue=connected && !(state["ipv4"] as? String ?? "").isEmpty ? "已连接 · \(state["ipv4"] as? String ?? "")":(phase=="stopped" ? "服务未运行，未核验":"等待连接");fields["internet"]?.textColor=connected ? .systemGreen:.secondaryLabelColor;fields["sample"]?.stringValue=dateText(state["status_observed"] as? String)
        notificationButton?.state=notifications ? .on:.off;purgeButton?.state=autoPurge ? .on:.off;notificationButton?.isEnabled=client.isReady;purgeButton?.isEnabled=client.isReady;soundButton?.state=sound ? .on:.off;permissionLabel?.stringValue=permissionGranted ? "系统通知已允许":"系统通知尚未允许"
        recentItem.title="查看最新短信\(unreadCount>0 ? "（\(unreadCount)）":"")"
        if let error=store.error,!errorShown{errorShown=true;alert("界面偏好",error)}
    }
    var unreadCount:Int {messages.filter{!store.data.read.contains($0.id)}.count}
    func scheduleHistory(){guard !historyScheduled else{return};historyScheduled=true;DispatchQueue.main.asyncAfter(deadline:.now()+0.35){[weak self] in self?.historyScheduled=false;self?.refreshHistory()}}
    func refreshHistory(append:Bool=false){guard client.isReady,!shuttingDown else{return};generation+=1;let g=generation,query=search.stringValue;loading=true;let offset=append ? messages.count:0
        client.request("messages",["search":query,"limit":200,"offset":offset]){[weak self] response in guard let self=self,self.generation==g else{return};self.loading=false;guard let raw=response["data"],let bytes=try? JSONSerialization.data(withJSONObject:raw),let rows=try? JSONDecoder().decode([SMS].self,from:bytes) else{self.count.stringValue="短信暂时无法读取，永久档案保留。";return};self.store.observe(rows);self.messages=append ? self.messages+rows:rows;self.more=rows.count==200;self.applyFilter();self.renderStatus()}}
    func controlTextDidChange(_ obj:Notification){scheduleHistory()}
    @objc func loadMore(){refreshHistory(append:true)}
    @objc func filterChanged(){applyFilter()}
    func applyFilter(){moreButton?.isHidden = !more;let selectedID=table.selectedRow>=0 && table.selectedRow<visible.count ? visible[table.selectedRow].id:nil;let choice=filters.selectedSegment;visible=messages.filter{m in if choice==1{return !store.data.read.contains(m.id)};if choice>=2{let days=choice==2 ? 0:choice==3 ? 6:29;let start=Calendar.current.date(byAdding:.day,value:-days,to:Calendar.current.startOfDay(for:Date()))!;return m.date>=start};return true}.sorted { $0.date == $1.date ? $0.id < $1.id : $0.date > $1.date };table.reloadData();if let id=selectedID,let i=visible.firstIndex(where:{$0.id==id}){table.selectRowIndexes(IndexSet(integer:i),byExtendingSelection:false)};count.stringValue=visible.isEmpty ? (search.stringValue.isEmpty ? "暂无符合条件的短信":"未找到匹配的号码或内容") : "共 \(visible.count) 条短信\(more ? " · 还有更多":"")"}
    func numberOfRows(in tableView:NSTableView)->Int{visible.count}
    func tableView(_ tableView:NSTableView,viewFor tableColumn:NSTableColumn?,row:Int)->NSView?{guard row<visible.count else{return nil};let m=visible[row];let cell=MessageCell();cell.sender.stringValue=m.sender.isEmpty ? "未知发送方":m.sender;cell.preview.stringValue=m.body.isEmpty ? (m.state=="incomplete" ? "分段短信，等待接收完整":"此短信暂无文本内容"):m.body.replacingOccurrences(of:"\n",with:" ");cell.stamp.stringValue="收到于 · \(store.stamp(m))";let f=DateFormatter();f.locale=Locale(identifier:"zh_CN");f.dateFormat=Calendar.current.isDateInToday(m.date) ? "HH:mm":"M月d日";cell.time.stringValue=f.string(from:m.date);cell.dot.isHidden=store.data.read.contains(m.id);cell.setAccessibilityLabel("\(m.sender)，\(cell.preview.stringValue)，\(cell.stamp.stringValue)");return cell}
    @objc func openSelected(){let row=table.selectedRow;guard row>=0,row<visible.count else{return};open(visible[row])}
    func open(_ m:SMS){client.request("message",["message_id":m.id]){[weak self] response in guard let self=self,let raw=response["data"],let bytes=try? JSONSerialization.data(withJSONObject:raw),let full=try? JSONDecoder().decode(SMS.self,from:bytes) else{return};self.showDetail(full)} }
    func showDetail(_ m:SMS){
        currentMessage=m;store.data.read.insert(m.id);store.save();applyFilter();renderStatus()
        if detailWindow==nil{let w=NSWindow(contentRect:NSRect(x:0,y:0,width:438,height:590),styleMask:[.titled,.closable,.resizable,.miniaturizable],backing:.buffered,defer:false);w.minSize=NSSize(width:390,height:460);w.title="DJISMS";w.isReleasedWhenClosed=false;w.center();detailWindow=w}
        let root=DetailRoot();detailWindow!.contentView=root
        let back=button("",#selector(closeDetail));back.image=symbol("chevron.left",16);back.isBordered=false;back.setAccessibilityLabel("返回短信列表");back.widthAnchor.constraint(equalToConstant:18).isActive=true
        let number=label(m.sender.isEmpty ? "未知发送方":m.sender,15,.semibold);number.maximumNumberOfLines=2;number.lineBreakMode = .byCharWrapping;number.cell?.wraps=true;number.setContentCompressionResistancePriority(.defaultLow,for:.horizontal);let senderInfo=stack([number,label(m.brand,12,.regular,.secondaryLabelColor)],spacing:3);number.widthAnchor.constraint(equalTo:senderInfo.widthAnchor).isActive=true
        let mark=button("标记为未读",#selector(markUnread));detailMark=mark
        let extra=NSPopUpButton();extra.pullsDown=true;extra.addItems(withTitles:["•••","复制发送方号码","复制短信内容"]);extra.target=self;extra.action=#selector(copyDetail(_:));extra.setAccessibilityLabel("短信操作");(extra.cell as? NSPopUpButtonCell)?.arrowPosition = .noArrow;extra.widthAnchor.constraint(equalToConstant:34).isActive=true;mark.widthAnchor.constraint(equalToConstant:90).isActive=true
        let header=stack([back,icon("person.crop.circle.fill",size:34,color:.tertiaryLabelColor),senderInfo,mark,extra],vertical:false,spacing:8);header.translatesAutoresizingMaskIntoConstraints=false;root.addSubview(header);senderInfo.setContentHuggingPriority(.defaultLow,for:.horizontal);senderInfo.setContentCompressionResistancePriority(.defaultLow,for:.horizontal)
        let when=label("短信\n\(dateText(m.sent_at ?? m.created_at))",12,.regular,.secondaryLabelColor);when.maximumNumberOfLines=2;when.alignment = .center;when.translatesAutoresizingMaskIntoConstraints=false;root.addSubview(when)
        let bubble=Surface();bubble.fill=NSColor(name:nil,dynamicProvider:{appearance in appearance.bestMatch(from:[.aqua,.darkAqua]) == .darkAqua ? NSColor(calibratedWhite:0.19,alpha:1):NSColor(calibratedRed:0.91,green:0.91,blue:0.94,alpha:1)});bubble.bordered=false;bubble.translatesAutoresizingMaskIntoConstraints=false;root.addSubview(bubble)
        let scroll=NSScrollView();scroll.hasVerticalScroller=true;scroll.drawsBackground=false;let text=NSTextView();text.isEditable=false;text.isSelectable=true;text.drawsBackground=false;text.font = .systemFont(ofSize:15);text.textColor = .labelColor;text.textContainerInset=NSSize(width:14,height:14);text.isVerticallyResizable=true;text.isHorizontallyResizable=false;text.autoresizingMask=[.width];text.textContainer?.widthTracksTextView=true;text.string=m.body.isEmpty ? "原始短信已永久保存，暂无可显示的文本。":m.body;scroll.documentView=text;pin(scroll,bubble);root.bodyText=text;let bodyHeight=bubble.heightAnchor.constraint(equalToConstant:90);bodyHeight.priority = .defaultLow;bodyHeight.isActive=true;root.bodyHeight=bodyHeight
        let grid=NSGridView();grid.rowSpacing=8;grid.columnSpacing=14;grid.xPlacement = .leading
        for (key,value) in [("发送时间",dateText(m.sent_at,full:true)),("接收时间",dateText(m.created_at,full:true)),("收到号码",store.stamp(m)),("类型",m.parts>1 ? "短信（\(m.parts) 段）":"短信"),("状态",m.state=="incomplete" ? "分段待齐，已保存":"已永久保存"),("存储位置","本机 · 模块副本按设置安全清理")]{let val=label(value,12,key=="收到号码" ? .semibold:.regular);val.setContentCompressionResistancePriority(.defaultLow,for:.horizontal);grid.addRow(with:[label(key,12,.regular,.secondaryLabelColor),val])};grid.column(at:0).width = 64;grid.translatesAutoresizingMaskIntoConstraints=false;root.addSubview(grid)
        NSLayoutConstraint.activate([header.leadingAnchor.constraint(equalTo:root.leadingAnchor,constant:14),header.trailingAnchor.constraint(equalTo:root.trailingAnchor,constant:-14),header.topAnchor.constraint(equalTo:root.topAnchor,constant:14),header.heightAnchor.constraint(greaterThanOrEqualToConstant:42),when.topAnchor.constraint(equalTo:header.bottomAnchor,constant:19),when.centerXAnchor.constraint(equalTo:root.centerXAnchor),bubble.leadingAnchor.constraint(equalTo:root.leadingAnchor,constant:25),bubble.trailingAnchor.constraint(equalTo:root.trailingAnchor,constant:-25),bubble.topAnchor.constraint(equalTo:when.bottomAnchor,constant:19),bubble.bottomAnchor.constraint(equalTo:grid.topAnchor,constant:-24),bubble.heightAnchor.constraint(greaterThanOrEqualToConstant:90),grid.leadingAnchor.constraint(equalTo:root.leadingAnchor,constant:25),grid.trailingAnchor.constraint(lessThanOrEqualTo:root.trailingAnchor,constant:-18),grid.bottomAnchor.constraint(lessThanOrEqualTo:root.bottomAnchor,constant:-25)])
        detailWindow?.makeKeyAndOrderFront(nil);NSApp.activate(ignoringOtherApps:true)
    }
    @objc func markUnread(){guard let m=currentMessage else{return};store.data.read.remove(m.id);store.save();detailMark?.title="已标记为未读";detailMark?.isEnabled=false;applyFilter();renderStatus()}
    @objc func closeDetail(){detailWindow?.close();showWindow()}
    @objc func copyDetail(_ sender:NSPopUpButton){guard let m=currentMessage else{return};let text=sender.indexOfSelectedItem==1 ? m.sender:m.body;NSPasteboard.general.clearContents();NSPasteboard.general.setString(text,forType:.string)}
    @objc func editNumber(){guard connected,!simID.isEmpty else{return};let id=simID;let panel=NSAlert();panel.messageText="修改本机显示号码";panel.informativeText="只修改这张 SIM 在 DJISMS 中的显示号码。此前短信的接收号码记录保持不变。";let input=NSTextField(string:simNumber);input.frame=NSRect(x:0,y:0,width:280,height:26);input.placeholderString="例如 +86 138 1234 5678";panel.accessoryView=input;panel.addButton(withTitle:"保存");panel.addButton(withTitle:"取消");panel.beginSheetModal(for:window){[weak self] result in guard let self=self,result == .alertFirstButtonReturn else{return};let v=input.stringValue.trimmingCharacters(in:.whitespacesAndNewlines);guard !v.isEmpty,v.count<=32,v.unicodeScalars.allSatisfy({CharacterSet(charactersIn:"+0123456789 -()").contains($0)}) else{self.alert("号码格式不正确","请填写不超过 32 个字符的电话号码。");return};self.store.data.numbers[id]=v;self.store.save();self.renderStatus()}}
    @objc func refreshDevice(){if !client.isRunning{startReceiver()}else{poll()}}
    @objc func preferencesChanged(){guard client.isReady else{return};let n=notificationButton?.state == .on,p=purgeButton?.state == .on;notificationButton?.isEnabled=false;purgeButton?.isEnabled=false;client.request("preferences",["preferences":["notifications":n,"auto_purge":p]]){[weak self] r in if r["error"] != nil{self?.alert("设置未保存","接收服务暂未确认设置，请稍后再试。")};self?.poll()};if n{checkPermission(request:true)}}
    @objc func soundChanged(_ sender:NSButton){sound=sender.state == .on;store.data.sound=sound;store.save()}
    @objc func loginChanged(_ sender:NSButton){do{if sender.state == .on{try SMAppService.mainApp.register()}else{try SMAppService.mainApp.unregister()}}catch{sender.state=SMAppService.mainApp.status == .enabled ? .on:.off;alert("登录启动未能设置","请在系统设置的登录项中允许 DJISMS，或稍后再试。")}}
    @objc func appearanceChanged(_ sender:NSPopUpButton){store.data.appearance=["system","light","dark"][sender.indexOfSelectedItem];store.save();applyAppearance()}
    func applyAppearance(){NSApp.appearance=store.data.appearance=="light" ? NSAppearance(named:.aqua):store.data.appearance=="dark" ? NSAppearance(named:.darkAqua):nil}
    @objc func noop(){}
    func checkPermission(request:Bool){let center=UNUserNotificationCenter.current();center.getNotificationSettings{[weak self] s in if s.authorizationStatus == .notDetermined && request{center.requestAuthorization(options:[.alert,.sound,.badge]){_,_ in self?.checkPermission(request:false)};return};DispatchQueue.main.async{self?.permissionKnown=true;self?.permissionGranted=s.authorizationStatus == .authorized || s.authorizationStatus == .provisional;self?.renderStatus()}}}
    func fetchNotifications(){guard permissionKnown,client.isReady,!shuttingDown else{return};client.request("notifications"){[weak self] response in guard let self=self,let raw=response["data"],let bytes=try? JSONSerialization.data(withJSONObject:raw),let list=try? JSONDecoder().decode([SMS].self,from:bytes) else{return};for m in list where !self.notificationInFlight.contains(m.id){self.notificationInFlight.insert(m.id);self.store.markNew([m]);if !self.notifications || !self.permissionGranted{self.ack(m.id,self.notifications ? "denied":"disabled","");continue};let content=UNMutableNotificationContent();content.title="DJISMS · \(m.sender.isEmpty ? "新短信":m.sender)";content.body=String(m.body.prefix(160));if self.sound{content.sound = .default};content.userInfo=["message_id":m.id];let request=UNNotificationRequest(identifier:"djisms-"+m.id,content:content,trigger:nil);UNUserNotificationCenter.current().add(request){error in DispatchQueue.main.async{self.ack(m.id,error==nil ? "submitted":"failed",error?.localizedDescription ?? "")}}}}}
    func ack(_ id:String,_ result:String,_ detail:String){client.request("notification_result",["message_id":id,"result":result,"detail":String(detail.prefix(500))]){[weak self] _ in self?.notificationInFlight.remove(id);self?.scheduleHistory()}}
    func userNotificationCenter(_ center:UNUserNotificationCenter,willPresent notification:UNNotification,withCompletionHandler completionHandler:@escaping(UNNotificationPresentationOptions)->Void){completionHandler(sound ? [.banner,.list,.sound]:[.banner,.list])}
    func userNotificationCenter(_ center:UNUserNotificationCenter,didReceive response:UNNotificationResponse,withCompletionHandler completionHandler:@escaping()->Void){DispatchQueue.main.async{self.showWindow();self.showPage(0);if let id=response.notification.request.content.userInfo["message_id"] as? String{self.client.request("message",["message_id":id]){r in if let raw=r["data"],let data=try? JSONSerialization.data(withJSONObject:raw),let m=try? JSONDecoder().decode(SMS.self,from:data){self.showDetail(m)}}}};completionHandler()}
    @objc func notificationSettings(){checkPermission(request:true);NSWorkspace.shared.open(URL(string:"x-apple.systempreferences:com.apple.Notifications-Settings.extension")!)}
    @objc func sleeping(){unavailable("Mac 即将睡眠，唤醒后自动恢复。",phase:"sleeping");client.request("power",["power":"prepare_sleep"])}
    @objc func waking(){unavailable("Mac 已唤醒，正在自动恢复连接。");client.request("power",["power":"awake"]);poll()}
    @objc func showWindow(){window.makeKeyAndOrderFront(nil);NSApp.activate(ignoringOtherApps:true)}
    @objc func latest(){showWindow();showPage(0);filters.selectedSegment=0;applyFilter();if let first=visible.first{open(first)}}
    @objc func device(){showWindow();showPage(1)}
    @objc func settings(){showWindow();showPage(2)}
    @objc func about(){showWindow();showPage(3)}
    @objc func help(){alert("DJISMS 使用帮助","连接 DJI 4G 模块后自动接收短信。\n\n所有短信永久保存在这台 Mac。清理模块副本前会完成保存与核验。关闭窗口仍接收，退出应用后停止。\n\n显示号码可在设备状态中修改；历史短信保留原接收记录。旧档案没有记录接收卡时会显示“未记录”。\n\n连接中断或 Mac 唤醒后会自动恢复。如果出现“接收已暂停”，请保留档案，退出后重新打开；若仍暂停，请联系支持进行检查。\n\n仅接收短信，不提供发送或回复。")}
    func alert(_ title:String,_ message:String){let a=NSAlert();a.messageText=title;a.informativeText=message;a.addButton(withTitle:"好");if let w=window,w.attachedSheet==nil{a.beginSheetModal(for:w)}else{a.runModal()}}
    @objc func quit(){NSApp.terminate(nil)}
    func applicationShouldTerminate(_ sender:NSApplication)->NSApplication.TerminateReply{if storageBusy{alert("正在完成 SIM 操作","请等待存储恢复及审计完成后退出。");return .terminateCancel};if !client.isRunning{return .terminateNow};if shuttingDown{return .terminateLater};shuttingDown=true;timer?.invalidate();statusText.stringValue="正在安全停止接收…";client.request("shutdown");return .terminateLater}
}
let app=NSApplication.shared
let delegate=AppDelegate()
app.delegate=delegate
app.run()
