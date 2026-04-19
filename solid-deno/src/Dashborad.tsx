import { Client, DefaultTrackMux } from "@okdaichi/moq";
import { PublishBoard } from "./publish/PublishBoard.tsx";
import { SubscribeBoard } from "./subscribe/SubscribeBoard.tsx";
import { createUsername } from "./user/user_name.ts";
import { UserController, UserProvider } from "./user/UserProvider.tsx";

export function Dashborad() {
	const { username, regenerate } = createUsername();
	const relayUrl = import.meta.env.VITE_RELAY_URL ?? "https://localhost:4433";
	const certHash = import.meta.env.VITE_CERT_HASH as string | undefined;

	const transportOptions: WebTransportOptions = {};
	if (certHash) {
		const bytes = new Uint8Array(certHash.length / 2);
		for (let i = 0; i < certHash.length; i += 2) {
			bytes[i / 2] = parseInt(certHash.substring(i, i + 2), 16);
		}
		transportOptions.serverCertificateHashes = [
			{ algorithm: "sha-256", value: bytes },
		];
		console.log("[client] using cert hash:", certHash);
	} else {
		console.warn("[client] VITE_CERT_HASH not set — run 'mage cert' to generate");
	}

	const mux = DefaultTrackMux;
	const client = new Client({ transportOptions });
	const session = client.dial(relayUrl, mux);
	session.catch((e) => console.error("[client] session failed:", e));
	return (
		<>
			<div
				style={{
					display: "flex",
					gap: "16px",
					"flex-direction": "column",
					"align-items": "center",
				}}
			>
				<div style={{ display: "flex", gap: "16px", "align-items": "center" }}>
					<UserController regenerate={regenerate} />
					<span>{username()}</span>
				</div>

				<UserProvider username={username}>
					<div style={{ display: "flex", gap: "16px" }}>
						<PublishBoard mux={mux} />
						<SubscribeBoard session={session} />
					</div>
				</UserProvider>
			</div>
		</>
	);
}
