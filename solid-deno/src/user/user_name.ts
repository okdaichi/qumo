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
