import { ArrayBufferTarget, Muxer } from "mp4-muxer";
import type { ByteSource } from "@qumo/moq";

// RawBytes is a MoQ object payload of bare bytes — no LOC timestamp framing.
// A CMAF group carries an fMP4 fragment verbatim, so the object is the fragment
// bytes, not a timestamped LOC frame ([MediaFrame]).
export class RawBytes implements ByteSource {
	readonly data: Uint8Array;
	constructor(data: Uint8Array) {
		this.data = data;
	}
	get byteLength(): number {
		return this.data.byteLength;
	}
	copyTo(target: ArrayBuffer | ArrayBufferView): void {
		const view = target instanceof ArrayBuffer
			? new Uint8Array(target)
			: new Uint8Array(target.buffer, target.byteOffset, target.byteLength);
		view.set(this.data);
	}
}

function u32(view: Uint8Array, offset: number): number {
	return (view[offset] * 0x1000000) +
		((view[offset + 1] << 16) | (view[offset + 2] << 8) | view[offset + 3]);
}

function fourcc(view: Uint8Array, offset: number): string {
	return String.fromCharCode(view[offset], view[offset + 1], view[offset + 2], view[offset + 3]);
}

// boxSize returns the size of an ISOBMFF box at offset, following the 8-byte
// extended size when the 4-byte size field is 1.
function boxSize(view: Uint8Array, offset: number): number {
	const size = u32(view, offset);
	if (size === 1) {
		// Sizes >2^32 won't happen for these dev-scale GOPs; guard anyway.
		const hi = u32(view, offset + 8);
		if (hi > 0) return view.length;
		return u32(view, offset + 12);
	}
	return size;
}

// splitInitFragment separates a fragmented MP4 buffer into its initialization
// (ftyp + moov) and the trailing fragment (moof + mdat). mp4-muxer with
// fastStart:false writes ftyp, moov, then the fragment boxes, so the init is
// every box up to the first moof.
export function splitInitFragment(buffer: ArrayBuffer): { init: Uint8Array; fragment: Uint8Array } {
	const view = new Uint8Array(buffer);
	let offset = 0;
	while (offset + 8 <= view.length) {
		const type = fourcc(view, offset + 4);
		if (type === "moof") break;
		const size = boxSize(view, offset);
		if (size <= 0) break;
		offset += size;
	}
	return {
		init: view.slice(0, offset),
		fragment: view.slice(offset),
	};
}

// CmafGopMuxer muxes one GOP (keyframe + its deltas) into a self-contained
// fragmented MP4 buffer, from which [splitInitFragment] extracts the init
// (moov, cached on the first GOP and carried in the catalog) and the fragment
// (moof+mdat, published as one group). The encoder's absolute chunk timestamps
// keep the fragments' decode times continuous across GOPs.
export class CmafGopMuxer {
	private muxer: Muxer<ArrayBufferTarget>;
	private target = new ArrayBufferTarget();

	constructor(width: number, height: number) {
		this.muxer = new Muxer({
			target: this.target,
			fastStart: false,
			video: { codec: "avc", width, height },
		});
	}

	addVideoChunk(chunk: EncodedVideoChunk): void {
		this.muxer.addVideoChunk(chunk);
	}

	finalize(): ArrayBuffer {
		this.muxer.finalize();
		return this.target.buffer;
	}
}
