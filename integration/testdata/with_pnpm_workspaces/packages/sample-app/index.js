const express = require('express');
const config = require('@sample/sample-config');
const app = express();
const port = process.env.PORT || 8080;

app.get('/', (req, res) => {
    res.send(`Package A value 1: ${config.prop1}, Package A value 2: ${config.prop2}`);
});

app.listen(port, () => {
    console.log(`Server listening on port ${port}`);
});