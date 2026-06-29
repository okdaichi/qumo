import { connect, DefaultTrackMux } from "@qumo/moq";
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
		const len = certHash.length;
		const bytes = new Uint8Array(len / 2);
		for (let i = 0; i < len; i += 2) {
			let hi = certHash.charCodeAt(i);
			let lo = certHash.charCodeAt(i + 1);
			hi = hi >= 97 ? hi - 87 : hi >= 65 ? hi - 55 : hi - 48;
			lo = lo >= 97 ? lo - 87 : lo >= 65 ? lo - 55 : lo - 48;
			bytes[i / 2] = (hi << 4) | lo;
		}
		transportOptions.serverCertificateHashes = [
			{ algorithm: "sha-256", value: bytes },
		];
		console.log("[client] using cert hash:", certHash);
	} else {
		console.warn("[client] VITE_CERT_HASH not set — run 'mage cert' to generate");
	}

	const mux = DefaultTrackMux;
	const session = connect(relayUrl, { mux, transportOptions });
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
