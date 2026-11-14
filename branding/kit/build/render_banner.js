const sharp = require('/tmp/node_modules/sharp');
const fs = require('fs');
const KIT = '/mnt/nas/project/contentremixer/branding/kit';
(async () => {
  const buf = fs.readFileSync(`${KIT}/svg/readme_banner.svg`);
  await sharp(buf, { density: 384 }).resize(1280, 320, { fit:'contain', background:{r:0,g:0,b:0,alpha:0}}).png().toFile(`${KIT}/png/readme_banner.png`);
  // also a 2x retina for docs
  await sharp(buf, { density: 384 }).resize(2560, 640, { fit:'contain', background:{r:0,g:0,b:0,alpha:0}}).png().toFile(`${KIT}/png/readme_banner@2x.png`);
  const m = await sharp(`${KIT}/png/readme_banner.png`).metadata();
  console.log(`readme_banner.png: ${m.width}x${m.height}`);
})();
