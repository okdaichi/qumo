import { createSignal } from "solid-js";

const adjectives = [
	"Happy",
	"Clever",
	"Brave",
	"Calm",
	"Swift",
	"Bright",
	"Cool",
	"Wise",
	"Bold",
	"Quick",
];

const animals = [
	"Panda",
	"Tiger",
	"Eagle",
	"Dolphin",
	"Fox",
	"Owl",
	"Wolf",
	"Bear",
	"Hawk",
	"Lion",
];

export function generateUsername(): string {
	const adjective = adjectives[Math.floor(Math.random() * adjectives.length)];
	const animal = animals[Math.floor(Math.random() * animals.length)];
	const number = Math.floor(Math.random() * 1000);

	return `${adjective}${animal}${number}`;
}

export function createUsername() {
	const [username, setUsername] = createSignal(generateUsername());

	function regenerate() {
		setUsername(generateUsername());
	}

	return {
		username,
		regenerate,
	};
}

// Short high-entropy token so each session's broadcast path is unique on a
// shared public relay, avoiding collisions between users. Uses the first two
// groups of a UUID v4 (48 random bits) — collision-negligible for a playground.
export function generateBroadcastId(): string {
	const groups = crypto.randomUUID().split("-");
	return groups[0] + groups[1];
}
