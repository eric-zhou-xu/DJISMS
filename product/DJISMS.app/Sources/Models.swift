import AppKit

struct SMS: Decodable {
    let id: String
    let state: String
    let sender: String
    let body: String
    let created_at: String
    let sent_at: String?
    let notification_state: String
    let parts: Int
    let sim_id: String?
    let sim_number: String?
    var date: Date { parseDate(sent_at ?? created_at) ?? .distantPast }
    var brand: String {
        guard body.first == "【", let end = body.firstIndex(of: "】") else { return "" }
        let name = String(body[body.index(after: body.startIndex)..<end]); return name.count <= 22 ? name : ""
    }
}
func parseDate(_ text: String) -> Date? {
    let f = ISO8601DateFormatter(); f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    if let d = f.date(from: text) { return d }; f.formatOptions = [.withInternetDateTime]; return f.date(from: text)
}
func dateText(_ text: String?, full: Bool = false) -> String {
    guard let text = text, let date = parseDate(text) else { return "未记录" }
    let f = DateFormatter(); f.locale = Locale(identifier: "zh_CN"); f.dateFormat = full ? "yyyy-MM-dd HH:mm:ss" : "M月d日 HH:mm"; return f.string(from: date)
}
struct SIMStamp: Codable { var id: String; var number: String }
struct UIData: Codable {
    var read = Set<String>()
    var known = Set<String>()
    var stamps: [String: SIMStamp] = [:]
    var numbers: [String: String] = [:]
    var sound = true
    var appearance = "system"
}
// Presentation data never writes the core's SQLite, raw files or journal.
final class UIStore {
    var data = UIData()
    private let url: URL
    private(set) var error: String?
    init(root: URL? = nil) {
        let base = root ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appendingPathComponent("DJISMS-UI", isDirectory: true)
        url = base.appendingPathComponent("presentation.json")
        do {
            try FileManager.default.createDirectory(at: base, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            if FileManager.default.fileExists(atPath: url.path) { data = try JSONDecoder().decode(UIData.self, from: Data(contentsOf: url)) }
        } catch { self.error = "界面偏好无法读取。原始短信仍安全保留。" }
    }
    func save() {
        guard error == nil else { return }
        do { let encoded = try JSONEncoder().encode(data); try encoded.write(to: url, options: .atomic); try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path) }
        catch { self.error = "界面偏好未能保存。原始短信仍安全保留。" }
    }
    func observe(_ messages: [SMS]) {
        var changed = false
        for m in messages where !data.known.contains(m.id) {
            data.known.insert(m.id)
            // Existing history does not become newly unread on first UI launch.
            if m.notification_state != "pending" { data.read.insert(m.id) }
            let sid = m.sim_id ?? ""
            data.stamps[m.id] = SIMStamp(id: sid, number: sid.isEmpty ? "" : (data.numbers[sid] ?? m.sim_number ?? ""))
            changed = true
        }
        if changed { save() }
    }
    func markNew(_ messages: [SMS]) {
        observe(messages)
        for m in messages { data.read.remove(m.id) }; save()
    }
    func number(id: String, reported: String) -> String { data.numbers[id] ?? reported }
    func stamp(_ m: SMS) -> String {
        guard let stamp = data.stamps[m.id], !stamp.id.isEmpty else { return "接收 SIM 未记录" }
        return stamp.number.isEmpty ? "SIM · \(stamp.id.prefix(8))" : stamp.number
    }
}

func label(_ text: String, _ size: CGFloat = 13, _ weight: NSFont.Weight = .regular, _ color: NSColor = .labelColor) -> NSTextField {
    let v = NSTextField(labelWithString: text); v.font = .systemFont(ofSize: size, weight: weight); v.textColor = color; v.lineBreakMode = .byTruncatingTail; return v
}
func symbol(_ name: String, _ size: CGFloat = 18) -> NSImage? { NSImage(systemSymbolName: name, accessibilityDescription: nil)?.withSymbolConfiguration(.init(pointSize: size, weight: .regular)) }
func icon(_ name: String, size: CGFloat = 20, color: NSColor = .labelColor) -> NSImageView {
    let v = NSImageView(); v.image = symbol(name, size); v.contentTintColor = color; v.translatesAutoresizingMaskIntoConstraints = false
    NSLayoutConstraint.activate([v.widthAnchor.constraint(equalToConstant: size+2),v.heightAnchor.constraint(equalToConstant: size+2)]); return v
}
func stack(_ views: [NSView], vertical: Bool = true, spacing: CGFloat = 12) -> NSStackView {
    let v = NSStackView(views: views); v.orientation = vertical ? .vertical : .horizontal; v.alignment = vertical ? .leading : .centerY; v.spacing = spacing; v.distribution = .fill; return v
}
func pin(_ child: NSView, _ parent: NSView, _ inset: CGFloat = 0) {
    child.translatesAutoresizingMaskIntoConstraints = false; parent.addSubview(child)
    NSLayoutConstraint.activate([child.leadingAnchor.constraint(equalTo: parent.leadingAnchor,constant:inset),child.trailingAnchor.constraint(equalTo:parent.trailingAnchor,constant:-inset),child.topAnchor.constraint(equalTo:parent.topAnchor,constant:inset),child.bottomAnchor.constraint(equalTo:parent.bottomAnchor,constant:-inset)])
}
final class Surface: NSView {
    var fill: NSColor = .controlBackgroundColor
    var bordered = true
    override func draw(_ rect: NSRect) {
        let p = NSBezierPath(roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: 8, yRadius: 8)
        fill.setFill();p.fill(); if bordered { NSColor.separatorColor.withAlphaComponent(0.12).setStroke();p.lineWidth=1;p.stroke() }
    }
    override func viewDidChangeEffectiveAppearance() { super.viewDidChangeEffectiveAppearance(); needsDisplay=true }
}
final class NavButton: NSButton {
    var active = false { didSet { needsDisplay=true; contentTintColor = active ? .controlAccentColor : .labelColor } }
    override func draw(_ rect: NSRect) {
        if active { NSColor.controlAccentColor.withAlphaComponent(0.17).setFill(); NSBezierPath(roundedRect: bounds.insetBy(dx:0,dy:1),xRadius:6,yRadius:6).fill() }
        super.draw(rect)
    }
}
final class MessageCell: NSTableCellView {
    let avatar = icon("person.crop.circle.fill", size: 36, color: .tertiaryLabelColor)
    let sender = label("",14,.semibold), preview = label("",12,.regular,.secondaryLabelColor), stamp = label("",10,.regular,.tertiaryLabelColor), time = label("",11,.regular,.secondaryLabelColor)
    let dot = icon("circle.fill",size:6,color:.controlAccentColor)
    override init(frame: NSRect) {
        super.init(frame:frame)
        let top=stack([sender,time],vertical:false,spacing:8);sender.setContentCompressionResistancePriority(.defaultLow,for:.horizontal);time.setContentHuggingPriority(.required,for:.horizontal)
        let text=stack([top,preview,stamp],spacing:3);text.translatesAutoresizingMaskIntoConstraints=false
        for x in [avatar,text,dot] { addSubview(x) }
        NSLayoutConstraint.activate([avatar.leadingAnchor.constraint(equalTo:leadingAnchor,constant:10),avatar.centerYAnchor.constraint(equalTo:centerYAnchor),text.leadingAnchor.constraint(equalTo:avatar.trailingAnchor,constant:10),text.trailingAnchor.constraint(equalTo:trailingAnchor,constant:-12),text.centerYAnchor.constraint(equalTo:centerYAnchor),top.widthAnchor.constraint(equalTo:text.widthAnchor),preview.widthAnchor.constraint(equalTo:text.widthAnchor),stamp.widthAnchor.constraint(equalTo:text.widthAnchor),dot.leadingAnchor.constraint(equalTo:leadingAnchor,constant:2),dot.centerYAnchor.constraint(equalTo:centerYAnchor)])
    }
    required init?(coder:NSCoder){fatalError()}
    override var backgroundStyle: NSView.BackgroundStyle { didSet { let selected=backgroundStyle == .emphasized;sender.textColor=selected ? .white:.labelColor;preview.textColor=selected ? .white.withAlphaComponent(0.9):.secondaryLabelColor;stamp.textColor=selected ? .white.withAlphaComponent(0.75):.tertiaryLabelColor;time.textColor=selected ? .white:.secondaryLabelColor;avatar.contentTintColor=selected ? .white.withAlphaComponent(0.8):.tertiaryLabelColor } }
}

final class FlippedView: NSView { override var isFlipped: Bool { true } }

// The bubble hugs short messages and yields space to the scroll view for long ones.
final class DetailRoot: NSView {
    weak var bodyText: NSTextView?
    var bodyHeight: NSLayoutConstraint?
    override func layout() {
        super.layout()
        guard let text=bodyText,let container=text.textContainer,let manager=text.layoutManager,let height=bodyHeight else{return}
        manager.ensureLayout(for:container)
        let measured=max(90,ceil(manager.usedRect(for:container).height+28))
        let desired=min(measured,max(90,bounds.height-310))
        if abs(height.constant-desired)>0.5 {height.constant=desired}
    }
}
