import Foundation
final class CoreClient {
    private var process: Process?
    private let input = Pipe()
    private let output = Pipe()
    private let errors = Pipe()
    private var buffer = Data()
    private var nextID = 0
    private var callbacks: [String: ([String: Any]) -> Void] = [:]
    var event: (([String: Any]) -> Void)?
    var exited: (() -> Void)?
    private(set) var isReady = false
    var isRunning: Bool { process?.isRunning == true }
    func launch() throws {
        guard let executable = Bundle.main.url(forAuxiliaryExecutable: "djisms-core") ?? Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/djisms-core") as URL? else {
            throw NSError(domain: "DJISMS", code: 1, userInfo: [NSLocalizedDescriptionKey: "缺少核心组件"])
        }
        let child = Process()
        child.executableURL = executable
        child.standardInput = input
        child.standardOutput = output
        child.standardError = errors
        // Never inherit a developer path or a shell. The helper resolves the
        // current user's Application Support directory itself.
        child.environment = ["HOME": FileManager.default.homeDirectoryForCurrentUser.path, "PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "LANG": "en_US.UTF-8"]
        output.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            DispatchQueue.main.async { self?.consume(data) }
        }
        errors.fileHandleForReading.readabilityHandler = { handle in _ = handle.availableData }
        child.terminationHandler = { [weak self] _ in DispatchQueue.main.async {
            guard let self = self else { return }
            self.isReady = false
            let pending = self.callbacks; self.callbacks.removeAll()
            for callback in pending.values { callback(["error": "核心服务已停止"]) }
            self.exited?()
        } }
        try child.run()
        process = child
    }
    private func consume(_ bytes: Data) {
        if bytes.isEmpty { return }
        buffer.append(bytes)
        if buffer.count > 2_000_000 {
            buffer.removeAll()
            event?(["event": "fatal", "error": "核心响应超出上限"])
            return
        }
        while let newline = buffer.firstIndex(of: 10) {
            let line = buffer.prefix(upTo: newline)
            buffer.removeSubrange(...newline)
            guard let value = try? JSONSerialization.jsonObject(with: line) as? [String: Any], value["version"] as? Int == 1 else {
                event?(["event": "fatal", "error": "核心协议不兼容"])
                continue
            }
            if value["event"] as? String == "hello" { isReady = true }
            if let id = value["id"] as? String, let callback = callbacks.removeValue(forKey: id) { callback(value) }
            else { event?(value) }
        }
    }
    func request(_ method: String, _ fields: [String: Any] = [:], completion: (([String: Any]) -> Void)? = nil) {
        guard process?.isRunning == true else { completion?(["error": "核心服务未运行"]); return }
        nextID += 1
        let id = String(nextID)
        var request = fields
        request["version"] = 1; request["id"] = id; request["method"] = method
        if let completion = completion { callbacks[id] = completion }
        DispatchQueue.main.asyncAfter(deadline: .now() + 15) { [weak self] in
            guard let self = self, let callback = self.callbacks.removeValue(forKey: id) else { return }
            callback(["error": "核心服务未及时响应，当前状态未知"])
        }
        guard var data = try? JSONSerialization.data(withJSONObject: request) else { return }
        data.append(10)
        do { try input.fileHandleForWriting.write(contentsOf: data) }
        catch { callbacks.removeValue(forKey: id)?(["error": "无法连接核心服务"]); event?(["event": "fatal", "error": "无法连接核心服务"] ) }
    }
}

