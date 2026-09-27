import { useCallback } from "react";
import { useSearchParams } from "react-router-dom";

// Opens the run drawer (?run=) on top of whatever page is showing, keeping
// the page's own parameters.
export function useOpenRun(): (runId: string) => void {
	const [, setParams] = useSearchParams();
	return useCallback(
		(runId: string) =>
			setParams((p) => {
				const n = new URLSearchParams(p);
				n.set("run", runId);
				return n;
			}),
		[setParams],
	);
}
