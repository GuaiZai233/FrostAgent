package llm

// Indices belong to the lane's private transcript, not shared session history.
// They are adjusted only at preparation boundaries or for the originating live
// turn after its model call has finished. All access requires the lane mutex.
type sequenceTranscriptTurn struct {
	inputID      uint64
	inputIndex   int
	finalIndex   int
	pendingFinal string
}

func (lane *executionSequence) recordFinalProgress(turn *SessionTurn, historyID uint64, text string) {
	if lane.proactive || !lane.messageIDs[turn.messageSeq] {
		return // This task no longer inherits the originating private trajectory.
	}
	for i := range lane.transcriptTurns {
		checkpoint := &lane.transcriptTurns[i]
		if checkpoint.inputID != turn.messageSeq {
			continue
		}
		// Claim even a late first delivery immediately. Otherwise it would be
		// prepended from shared history before its retained originating input.
		lane.messageIDs[historyID] = true
		checkpoint.pendingFinal = text
		if lane.active == turn {
			lane.applyFinalProgress(i)
		}
		return
	}
}

func (lane *executionSequence) applyPendingFinalProgress() {
	for i := range lane.transcriptTurns {
		lane.applyFinalProgress(i)
	}
}

func (lane *executionSequence) applyFinalProgress(index int) {
	checkpoint := &lane.transcriptTurns[index]
	if checkpoint.pendingFinal == "" {
		return
	}
	message := ChatMessage{Role: "assistant", Content: checkpoint.pendingFinal}
	if checkpoint.finalIndex >= 0 {
		lane.transcript[checkpoint.finalIndex] = message
	} else {
		// Final text follows its own input/tool trajectory and precedes the
		// next turn's input, regardless of transport confirmation order.
		position := len(lane.transcript)
		if index+1 < len(lane.transcriptTurns) {
			position = lane.transcriptTurns[index+1].inputIndex
		}
		lane.transcript = append(lane.transcript, ChatMessage{})
		copy(lane.transcript[position+1:], lane.transcript[position:])
		lane.transcript[position] = message
		checkpoint.finalIndex = position
		for i := index + 1; i < len(lane.transcriptTurns); i++ {
			lane.transcriptTurns[i].inputIndex++
			if lane.transcriptTurns[i].finalIndex >= 0 {
				lane.transcriptTurns[i].finalIndex++
			}
		}
	}
	checkpoint.pendingFinal = ""
}
