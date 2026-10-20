import QtQuick
import Quickshell
import Quickshell.Io

Item {
  id: root
  readonly property string socketPath: (Quickshell.env("XDG_RUNTIME_DIR") || "") + "/file-converter/fcd.sock"
  property var jobs: ({})
  property bool connected: false
  property int requestId: 1
  property string buffer: ""
  property int phase: 0

  readonly property int activeCount: Object.keys(jobs).length
  readonly property int runningCount: {
    var count = 0
    for (var id in jobs) if (!jobs[id].finishedAt) count++
    return count
  }
  readonly property int filesTotal: {
    var count = 0
    for (var id in jobs) count += (jobs[id].files || []).length
    return count
  }
  readonly property int filesDone: {
    var count = 0
    for (var id in jobs) {
      var files = jobs[id].files || []
      for (var i = 0; i < files.length; i++) if (files[i].state !== "pending" && files[i].state !== "running") count++
    }
    return count
  }
  readonly property int completedCount: {
    var count = 0
    for (var id in jobs) if (jobs[id].state === "completed" || jobs[id].state === "completed_with_errors" || jobs[id].state === "failed" || jobs[id].state === "cancelled") count++
    return count
  }
  readonly property real overallProgress: {
    var total = 0, progress = 0
    for (var id in jobs) { total++; progress += Number(jobs[id].progress || 0) }
    return total ? progress / total : 0
  }

  function start() {
    if (socketPath === "/file-converter/fcd.sock") return
    connector.running = true
  }
  function cancelJob(id) {
    if (!connector.running) return
    connector.write(JSON.stringify({ id: requestId++, method: "cancel", params: { job: String(id) } }) + "\n")
  }
  function parseLine(line) {
    var message
    try { message = JSON.parse(line) } catch (error) { return }
    if (message.event === "job.updated" || message.event === "job.finished") {
      var job = message.data || {}
      var next = {}
      for (var id in root.jobs) next[id] = root.jobs[id]
      // Keep a finished job visible briefly so quick conversions do not just flash by.
      if (message.event === "job.finished") job.finishedAt = Date.now()
      next[job.id] = job
      root.jobs = next
      if (job.finishedAt) prune.restart()
    }
    if (message.result && message.result.server === "fcd") root.connected = true
  }

  Process {
    id: connector
    command: ["python3", Qt.resolvedUrl("progress_bridge.py").toString().replace("file://", "")]
    running: false
    stdinEnabled: true
    stdout: SplitParser { onRead: function(line) { root.parseLine(line) } }
    stderr: SplitParser { onRead: function(line) { console.warn("file-converter bridge:", line) } }
    onExited: { root.connected = false; reconnect.restart() }
    Component.onCompleted: root.start()
  }
  Timer {
    id: prune
    interval: 4000
    repeat: false
    onTriggered: {
      var next = {}
      var pending = false
      for (var id in root.jobs) {
        var job = root.jobs[id]
        if (job.finishedAt && Date.now() - job.finishedAt >= 3900) continue
        if (job.finishedAt) pending = true
        next[id] = job
      }
      root.jobs = next
      if (pending) prune.restart()
    }
  }
  Timer {
    id: reconnect
    interval: 3000
    repeat: false
    onTriggered: root.start()
  }
}
