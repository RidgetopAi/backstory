import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui
import qs.Commons
import qs.Commons as Commons
import "js/launchers.js" as Launchers
import "js/glyphs.js" as Glyphs

// Help pane (the "?" key): the agent's tools, how to view memories, how to
// use Backstory, and groups. A CLI CLIENT like the rest of the panel: the
// tool list is whatever `backstory tools --json` prints (the Go ToolsV0()
// the MCP server itself serves), so no tool name or description is written
// in this plugin. Swapped in for the This Week body by Panel.qml, the same
// pattern as GroupEditor and MemoryView; refetched whenever it is shown.
Item {
  id: root

  signal closeRequested()
  // Where the view is shown: the bar popup sets canPopOut, the window
  // canPin and pinned; the header then offers those buttons (HostButtons).
  property bool canPopOut: false
  property bool canPin: false
  property bool pinned: false
  signal popOutRequested()
  signal pinRequested()

  property var tools: []
  property string errorText: ""

  implicitHeight: page.implicitHeight
  // The scrolling part under the header (ScrollPage), for the host and tests.
  readonly property alias flickable: page.flickable

  // Help is text, not a list: j/k scroll it.
  function scrollBy(dy) { page.scrollBy(dy) }

  function refresh() {
    toolsProcess.running = false
    toolsProcess.running = true
  }

  onVisibleChanged: {
    if (root.visible) root.refresh()
  }

  Process {
    id: toolsProcess
    command: Launchers.toolsCommand()
    stdout: StdioCollector {
      onStreamFinished: {
        try {
          var parsed = JSON.parse(text || "{}")
          root.tools = parsed.tools || []
          root.errorText = ""
        } catch (e) {
          root.tools = []
          root.errorText = "Could not read the tool list from `backstory tools --json`."
        }
      }
    }
  }

  component HelpHeading: PanelSectionHeader {
    foreground: Commons.Color.foreground
    width: page.width
  }

  component HelpText: Text {
    width: page.width
    textFormat: Text.PlainText
    font.family: Style.font.family
    font.pixelSize: Style.font.bodySmall
    color: Commons.Color.foreground
    wrapMode: Text.WordWrap
  }

  // The header and the rule under it stay put; the rest scrolls.
  ScrollPage {
    id: page
    anchors.fill: parent
    spacing: Style.spacing.panelGap

    header: [HeroHeader {
      showBack: true
      title: "Help"
      status: "Keys, agent tools and groups"
      onBack: root.closeRequested()

      HostButtons {
        canPopOut: root.canPopOut
        canPin: root.canPin
        pinned: root.pinned
        onPopOutRequested: root.popOutRequested()
        onPinRequested: root.pinRequested()
      }
    },

    PanelSeparator { width: parent.width; foreground: Commons.Color.popups.text }]

    HelpHeading { text: "AGENT TOOLS" }

    HelpText {
      visible: root.errorText !== ""
      text: root.errorText
    }

    Repeater {
      model: root.tools

      delegate: Column {
        required property var modelData
        width: page.width
        spacing: Style.spacing.labelGap / 2

        Text {
          textFormat: Text.PlainText
          text: modelData.name
          font.family: Style.font.family
          font.pixelSize: Style.font.body
          font.bold: true
          color: Commons.Color.foreground
        }
        HelpText { text: modelData.description }
      }
    }

    PanelSeparator { width: parent.width; foreground: Commons.Color.popups.text }
    HelpHeading { text: "MEMORY NAVIGATION" }
    HelpText {
      text: "Select a row with j/k, then press m or click its Memory button. Click a record, or move "
        + "to it with j/k and press \u2192, to expand it; \u2190 folds it. Edit, Forget and the "
        + "history toggle act on it."
    }

    PanelSeparator { width: parent.width; foreground: Commons.Color.popups.text }
    HelpHeading { text: "HOW TO USE" }
    HelpText {
      text: "The top card is the project you are in. Each row's chips are the agents used there, "
        + "newest first. Continue resumes the newest one; \u2192 (or a click on the chips) expands one "
        + "sub-row per agent, \u2190 folds them, and Enter on a sub-row opens that agent in the folder. "
        + "Enter on a row continues it, t opens a terminal there, r refreshes. In Groups, Memory "
        + "and Help, u goes back to the main view."
    }

    PanelSeparator { width: parent.width; foreground: Commons.Color.popups.text }
    HelpHeading { text: "GROUPS" }
    HelpText {
      text: "A group folds related projects into one RECENT row; you set it, one per project. Press g "
        + "or click the gear for the group editor, or run `backstory group set <group> <project>`."
    }

  }
}
