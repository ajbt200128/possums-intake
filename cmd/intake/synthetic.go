package main

import "os"

// Removal is permitted only after joined work and confirmed storage closure.
// Uncertain state remains in runtime temporary storage for private inspection.
func cleanupScratch(dir string, safe bool) error {
	if !safe {
		return runtimeFailure(codeScratchCleanup, "scratch_cleanup", "confirmed_storage_and_worker_teardown", "unknown", "Temporary state was preserved because safe teardown is unconfirmed. Inspect runtime temporary storage privately; do not reuse it as ledger state.")
	}
	if err := os.RemoveAll(dir); err != nil {
		return runtimeFailure(codeScratchCleanup, "scratch_cleanup", "temporary_directory_removed", "unknown", "Temporary state removal is incomplete or unconfirmed; cause unknown. Inspect runtime temporary storage privately after shutdown.")
	}
	return nil
}
