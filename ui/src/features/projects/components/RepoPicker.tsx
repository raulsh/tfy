import { App, Button, Input, Select, Space } from "antd";
import { Lock } from "lucide-react";
import { useEffect, useState } from "react";
import { useGitHubOwners, useGitHubRepos, useLinkRepo } from "../hooks";

// Pick a GitHub repository by owner, from what gh can see, or type
// owner/name directly.
export function RepoPicker({ projectId, linked }: { projectId: string; linked: string[] }) {
	const [owner, setOwner] = useState<string>();
	const [repo, setRepo] = useState<string>();
	const [manual, setManual] = useState("");
	const owners = useGitHubOwners(true);
	const repos = useGitHubRepos(owner);
	const link = useLinkRepo(projectId);
	const { message } = App.useApp();

	useEffect(() => {
		if (!owner && owners.data?.length) setOwner(owners.data[0]);
	}, [owner, owners.data]);

	const add = (fullName: string) =>
		link.mutate(fullName, {
			onSuccess: () => {
				message.success(`${fullName} linked; cloning in the background`);
				setRepo(undefined);
				setManual("");
			},
			onError: (e) => message.error(e.message),
		});

	return (
		<div style={{ display: "grid", gap: 10 }}>
			<Space.Compact style={{ width: "100%" }}>
				<Select
					style={{ width: 200 }}
					placeholder="Owner"
					loading={owners.isLoading}
					value={owner}
					onChange={(v) => {
						setOwner(v);
						setRepo(undefined);
					}}
					options={(owners.data ?? []).map((o) => ({ value: o, label: o }))}
					status={owners.error ? "error" : undefined}
				/>
				<Select
					style={{ flex: 1 }}
					showSearch
					placeholder={owners.error ? "gh could not list repositories" : "Search repositories"}
					loading={repos.isLoading}
					value={repo}
					onChange={setRepo}
					optionFilterProp="label"
					options={(repos.data ?? [])
						.filter((r) => !linked.includes(r.full_name))
						.map((r) => ({
							value: r.full_name,
							label: r.full_name,
							desc: r.description,
							private: r.private,
						}))}
					optionRender={(o) => (
						<div>
							<div style={{ display: "flex", alignItems: "center", gap: 6 }}>
								{o.data.private && <Lock size={12} />}
								{o.data.label}
							</div>
							{o.data.desc && (
								<div className="faint" style={{ fontSize: 12, overflow: "hidden", textOverflow: "ellipsis" }}>
									{o.data.desc}
								</div>
							)}
						</div>
					)}
				/>
				<Button type="primary" disabled={!repo} loading={link.isPending} onClick={() => repo && add(repo)}>
					Link
				</Button>
			</Space.Compact>
			<Space.Compact style={{ width: "100%" }}>
				<Input
					placeholder="…or type owner/name"
					value={manual}
					onChange={(e) => setManual(e.target.value)}
					onPressEnter={() => manual && add(manual)}
				/>
				<Button disabled={!manual.includes("/")} loading={link.isPending} onClick={() => add(manual)}>
					Link
				</Button>
			</Space.Compact>
		</div>
	);
}
