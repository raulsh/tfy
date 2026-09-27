import { Spin } from "antd";
import { Link } from "react-router-dom";
import type { UnitDetail } from "@/shared/api/types";
import { Markdown } from "@/shared/components/misc";
import { Card } from "@/shared/components/OverlayDrawer";
import { useDocument } from "../hooks";

// What the finished unit's retrospective found, and the unit it opened to
// change the conventions, if any.
export function RetrospectiveCard({ unit }: { unit: UnitDetail }) {
	const has = !!unit.documents.retrospective;
	const doc = useDocument(has ? unit.id : undefined, "retrospective");
	if (!has) return null;
	const meta = doc.data?.document.meta as { suggested_unit_id?: string; suggested_unit?: string } | undefined;
	return (
		<Card
			title="Retrospective"
			extra={
				meta?.suggested_unit_id && <Link to={`/units/${meta.suggested_unit_id}`}>Open {meta.suggested_unit} →</Link>
			}
		>
			{doc.data ? <Markdown>{doc.data.document.content}</Markdown> : <Spin size="small" />}
		</Card>
	);
}
