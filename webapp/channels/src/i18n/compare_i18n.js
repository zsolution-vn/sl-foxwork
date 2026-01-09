
const fs = require('fs');
const path = require('path');

const enPath = path.join(__dirname, 'en.json');
const viPath = path.join(__dirname, 'vi.json');

const enData = JSON.parse(fs.readFileSync(enPath, 'utf8'));
const viData = JSON.parse(fs.readFileSync(viPath, 'utf8'));

const missingKeys = {};
const untranslatedKeys = {};

for (const key in enData) {
    if (!viData.hasOwnProperty(key)) {
        missingKeys[key] = enData[key];
    } else if (viData[key] === enData[key]) {
        // Simple heuristic: if the string is long enough and identical, it might be untranslated.
        // Short strings like "OK" or "ID" might be correct.
        if (enData[key].length > 5) {
            untranslatedKeys[key] = enData[key];
        }
    }
}

// console.log('Missing Keys:', JSON.stringify(missingKeys, null, 2));
// Only print a few untranslated ones to check, as there might be many false positives
console.log('Untranslated Keys (Identical):', JSON.stringify(untranslatedKeys, null, 2));
