const sharp = require('/tmp/node_modules/sharp');
const fs = require('fs');
const path = require('path');
const KIT = '/mnt/nas/project/contentremixer/branding/kit';
const SVG = path.join(KIT, 'svg');
const PNG = path.join(KIT, 'png');
const FAV = path.join(KIT, 'favicon');

async function render(svgName, outPath, w, h) {
  const buf = fs.readFileSync(path.join(SVG, svgName));
  let img = sharp(buf, { density: 384 });
  if (w) img = img.resize(w, h, { fit: 'contain', background: { r:0,g:0,b:0,alpha:0 } });
  await img.png().toFile(outPath);
  const meta = await sharp(outPath).metadata();
  console.log(`${path.basename(outPath)}: ${meta.width}x${meta.height}`);
}

(async () => {
  // full-size PNG exports (native canvas)
  await render('logo_master.svg',    path.join(PNG,'logo_master.png'),    512, 512);
  await render('logo_tagline.svg',   path.join(PNG,'logo_tagline.png'),   512, 512);
  await render('icon.svg',           path.join(PNG,'icon.png'),           512, 512);
  await render('logo_mono_white.svg',path.join(PNG,'logo_mono_white.png'),512, 512);
  await render('logo_mono_dark.svg', path.join(PNG,'logo_mono_dark.png'), 512, 512);
  await render('og_image.svg',       path.join(PNG,'og_image.png'),       1200, 630);

  // favicon ladder
  for (const s of [512, 192, 64, 32]) {
    await render('favicon_full.svg', path.join(FAV, `favicon_${s}.png`), s, s);
  }
  await render('favicon_min.svg', path.join(FAV, 'favicon_16.png'), 16, 16);
  console.log('DONE');
})().catch(e => { console.error(e); process.exit(1); });
