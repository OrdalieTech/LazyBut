package tui

import (
	_ "embed"
	"encoding/json"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/OrdalieTech/LazyBut/internal/gitbutler"
)

//go:embed testdata/demo_status.json
var demoStatusJSON []byte

// demoWorkspace returns a synthetic workspace snapshot suitable for screenshots
// and demos. The fixture covers a wide range of states (synced, ahead, force
// push, integrated, conflict, CI pass/fail, PR ids, mixed change types) so a
// single capture showcases the UI's full vocabulary.
func demoWorkspace() (*gitbutler.WorkspaceStatus, error) {
	var status gitbutler.WorkspaceStatus
	if err := json.Unmarshal(demoStatusJSON, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// renderDemoSnapshot builds a fully synthetic frame using the embedded demo
// fixture — no `but` call, no real git state. The optional `overlay` opens a
// modal on top (palette, upstream, picker, …) for screenshots that showcase
// modals.
func renderDemoSnapshot(width, height int, overlay string) string {
	lipgloss.SetColorProfile(termenv.ANSI256)
	status, err := demoWorkspace()
	if err != nil {
		m := newModel(nil)
		m.width = width
		m.height = height
		m.loading = false
		m.err = err
		return m.View()
	}
	if status.UpstreamState.LastFetched != nil {
		now := time.Now().Add(-12 * time.Minute).Format(time.RFC3339)
		status.UpstreamState.LastFetched = &now
	}
	model := newModel(&gitbutler.Client{Dir: "~/code/stardust"})
	model.width = width
	model.height = height
	model.loading = false
	model.data = buildWorkspaceData(status, nil)
	model.clampCursors()
	model.laneCursor = 0
	model.contentCursor = 0
	model, _ = model.withPreview()
	model.preview = demoPreviewDiff
	model.previewErr = nil
	model.previewTarget = "demo"

	switch overlay {
	case "palette":
		model.mode = modePalette
		model.palette = model.availableActions()
	case "upstream":
		model.mode = modeConfirm
		model.confirm = confirmState{Action: action{ID: actionPull, Label: "update from upstream"}}
	case "picker":
		// Stage-to-branch picker — focus a change so the picker is populated.
		stage := model.actionByID(actionStage)
		updated, _ := model.startAction(stage)
		if mm, ok := updated.(Model); ok {
			model = mm
		}
	}
	return model.View()
}

// demoPreviewDiff is a hand-crafted diff used as the preview body when the
// demo snapshot is rendered, so the capture doesn't show "loading diff…".
const demoPreviewDiff = `   42 42│ import { useState, useCallback } from 'react'
   43 43│ import { trpc } from '@stardust/api-client'
   44 44│
      45│+import { Toolbar } from './Toolbar'
      46│+import { useInlineEdit } from './hooks/useInlineEdit'
   45 47│
   46 48│ export interface InlineEditorProps {
   47 49│   value: string
   48   │-  onSave: (v: string) => void
      50│+  onSave: (v: string) => Promise<void>
      51│+  placeholder?: string
   49 52│ }
   50 53│
   51 54│ export function InlineEditor({ value, onSave }: InlineEditorProps) {
   52   │-  const [draft, setDraft] = useState(value)
      55│+  const [draft, setDraft] = useState(value)
      56│+  const { editing, beginEdit, commit } = useInlineEdit(draft)
   53 57│
   54   │-  return <input value={draft} onChange={e => setDraft(e.target.value)} />
      58│+  if (!editing) {
      59│+    return <Toolbar value={draft} onEdit={beginEdit} />
      60│+  }
      61│+  return (
      62│+    <input
      63│+      autoFocus
      64│+      value={draft}
      65│+      onChange={e => setDraft(e.target.value)}
      66│+      onBlur={() => commit(onSave)}
      67│+    />
      68│+  )
   55 69│ }
`
