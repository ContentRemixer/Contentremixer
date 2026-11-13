const sharp = require('/tmp/node_modules/sharp');
const fs = require('fs');
const KIT = '/tmp/isi7381-wt2/branding/kit';
(async () => {
  const buf = fs.readFileSync(`${KIT}/svg/badge_strip.svg`);
  await sharp(buf, { density: 288 }).png().toFile(`${KIT}/png/badge_strip.png`);
  console.log('rendered');
})().catch(e=>{console.error(e.message);process.exit(1)});
