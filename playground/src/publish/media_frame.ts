import type { ByteSource } from "@qumo/moq";
export class MediaFrame implements ByteSource {
	timestamp: number;
	source: ByteSource;

	constructor(init: { timestamp: number } & ByteSource) {
		this.timestamp = init.timestamp;
		this.source = init;
	}

	get byteLength(): number {
		return varintLen(this.timestamp) +
			varintLen(this.source.byteLength) +
			this.source.byteLength;
	}

	copyTo(target: ArrayBuffer | ArrayBufferView): void {
		let offset = 0;
		let buffer: Uint8Array;
		if (target instanceof ArrayBuffer) {
			buffer = new Uint8Array(target);
		} else {
			buffer = new Uint8Array(target.buffer, target.byteOffset, target.byteLength);
		}

		if (buffer.byteLength < this.byteLength) {
			throw new RangeError("Target buffer is too small");
		}

		offset += setVarint(this.timestamp, buffer.subarray(offset));
		offset += setVarint(this.source.byteLength, buffer.subarray(offset));
		this.source.copyTo(buffer.subarray(offset));
	}
}

function setVarint(num: number, target: Uint8Array): number {
	if (num < 0) {
		throw new Error("Varint cannot be negative");
	}

	let buf: Uint8Array;
	if (num <= MAX_VARINT1) {
		buf = new Uint8Array([num]);
	} else if (num <= MAX_VARINT2) {
		buf = new Uint8Array(2);
		buf[0] = (num >> 8) | 0x40;
		buf[1] = num & 0xff;
	} else if (num <= MAX_VARINT4) {
		buf = new Uint8Array(4);
		buf[0] = (num >> 24) | 0x80;
		buf[1] = (num >> 16) & 0xff;
		buf[2] = (num >> 8) & 0xff;
		buf[3] = num & 0xff;
	} else {
		// JavaScript bitwise operations are limited to 32 bits,
		// so use division for shifts exceeding 32 bits
		buf = new Uint8Array(8);
		buf[0] = Math.floor(num / 0x100000000000000) | 0xc0;
		buf[1] = Math.floor(num / 0x1000000000000) & 0xff;
		buf[2] = Math.floor(num / 0x10000000000) & 0xff;
		buf[3] = Math.floor(num / 0x100000000) & 0xff;
		buf[4] = Math.floor(num / 0x1000000) & 0xff;
		buf[5] = Math.floor(num / 0x10000) & 0xff;
		buf[6] = Math.floor(num / 0x100) & 0xff;
		buf[7] = num & 0xff;
	}

	target.set(buf, 0);
	return buf.length;
}

const MAX_VARINT1: number = (1 << 6) - 1; // 63
const MAX_VARINT2: number = (1 << 14) - 1; // 16383
const MAX_VARINT4: number = (1 << 30) - 1; // 1073741823

export function varintLen(value: number | bigint): number {
	// Handle negative values by converting to unsigned
	if (value < 0) {
		value = BigInt(value) + (1n << 64n);
	} else {
		value = BigInt(value);
	}

	if (value <= MAX_VARINT1) {
		return 1;
	} else if (value <= MAX_VARINT2) {
		return 2;
	} else if (value <= MAX_VARINT4) {
		return 4;
	} else {
		return 8;
	}
}

function readVarint(buffer: Uint8Array, offset: number): number {
	if (offset >= buffer.length) {
		throw new RangeError("Buffer overflow while reading varint");
	}

	const first = buffer[offset];
	const prefix = first >> 6;

	let value: number = 0;

	switch (prefix) {
		case 0: // 1 byte
			value = first & 0x3f;
			break;
		case 1: // 2 bytes
			if (offset + 1 >= buffer.length) {
				throw new RangeError("Buffer overflow while reading varint");
			}
			value = ((first & 0x3f) << 8) | buffer[offset + 1];
			break;
		case 2: // 4 bytes
			if (offset + 3 >= buffer.length) {
				throw new RangeError("Buffer overflow while reading varint");
			}
			value = ((first & 0x3f) << 24) |
				(buffer[offset + 1] << 16) |
				(buffer[offset + 2] << 8) |
				buffer[offset + 3];
			break;
		case 3: // 8 bytes
			if (offset + 7 >= buffer.length) {
				throw new RangeError("Buffer overflow while reading varint");
			}
			// Use division to handle 64-bit values since JavaScript bitwise ops are 32-bit
			value = ((first & 0x3f) * 0x100000000000000) +
				(buffer[offset + 1] * 0x1000000000000) +
				(buffer[offset + 2] * 0x10000000000) +
				(buffer[offset + 3] * 0x100000000) +
				(buffer[offset + 4] * 0x1000000) +
				(buffer[offset + 5] * 0x10000) +
				(buffer[offset + 6] * 0x100) +
				buffer[offset + 7];
			break;
	}

	return value;
}

export function deserializeMediaFrame(buffer: Uint8Array): { timestamp: number; data: Uint8Array } {
	let offset = 0;

	// Read timestamp
	const timestamp = readVarint(buffer, offset);
	offset += varintLen(timestamp);

	// Read byteLength
	const byteLength = readVarint(buffer, offset);
	offset += varintLen(byteLength);

	// Validate remaining buffer size
	if (offset + byteLength > buffer.length) {
		throw new RangeError("Buffer overflow: not enough bytes for frame data");
	}

	// Extract frame data
	const data = buffer.slice(offset, offset + byteLength);

	return { timestamp, data };
}
