// Pictures are cropped and scaled in the browser before uploading: the browser decodes formats
// the server doesn't (e.g. HEIC on iPhones), and the upload stays small. The server checks and
// re-encodes the result anyway.

/** The centred square of an image file as a JPEG of at most `size` pixels. */
export async function squareJpeg(file: File, size = 512): Promise<Blob> {
	const bitmap = await createImageBitmap(file);
	const side = Math.min(bitmap.width, bitmap.height);
	if (side < 128) throw new Error('too small');
	const out = Math.min(side, size);
	const canvas = document.createElement('canvas');
	canvas.width = canvas.height = out;
	const ctx = canvas.getContext('2d');
	if (!ctx) throw new Error('no canvas');
	ctx.imageSmoothingQuality = 'high';
	ctx.drawImage(bitmap, (bitmap.width - side) / 2, (bitmap.height - side) / 2, side, side, 0, 0, out, out);
	bitmap.close();
	return await new Promise((resolve, reject) =>
		canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('encode'))), 'image/jpeg', 0.9)
	);
}
