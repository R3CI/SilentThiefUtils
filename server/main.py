import os
import io
import json
import base64
import logging
import sys
import requests
from flask import Flask, request, jsonify
from waitress import serve

with open("config.json") as f:
    CONFIG = json.load(f)

CARD_BOT_TOKEN = CONFIG["card_bot_id"]
CARD_CHAT_ID   = CONFIG["card_chat_id"]
PORT      = int(os.environ.get("PORT", 5000))

app = Flask(__name__)
logging.basicConfig(level=logging.INFO, format="%(asctime)s %(message)s", stream=sys.stdout)
log = logging.getLogger(__name__)

@app.post("/v1/card_send")
def send():
    TG = f"https://api.telegram.org/bot{CARD_BOT_TOKEN}"
    body = request.get_json(silent=True) or {}
    text  = body.get("message", "").strip()
    image = body.get("image")

    if not text and not image:
        return jsonify({"ok": False, "error": "provide 'message' and/or 'image'"}), 400

    try:
        if image:
            raw = base64.b64decode(image)
            r = requests.post(f"{TG}/sendPhoto", data={
                "chat_id": CARD_CHAT_ID,
                "caption": text or None,
            }, files={"photo": ("image.jpg", io.BytesIO(raw), "image/jpeg")}, timeout=15)
        else:
            r = requests.post(f"{TG}/sendMessage", json={
                "chat_id": CARD_CHAT_ID,
                "text":    text,
            }, timeout=15)

        data = r.json()
        if data.get("ok"):
            log.info(f"sent ok -> msg_id={data['result']['message_id']}")
            return jsonify({"ok": True, "message_id": data["result"]["message_id"]})
        else:
            log.warning(f"telegram error: {data.get('description')}")
            return jsonify({"ok": False, "error": data.get("description")}), 502

    except Exception as e:
        log.error(e)
        return jsonify({"ok": False, "error": str(e)}), 500

try:
    public_ip = requests.get("https://api.ipify.org", timeout=5).text.strip()
except Exception:
    public_ip = "unavailable"

print(f"Local:   http://127.0.0.1:{PORT}")
print(f"Network: http://{public_ip}:{PORT}")
print('')
print(f'Put this as website URL on builders -> http://{public_ip}:{PORT}')
serve(app, host="0.0.0.0", port=PORT, threads=4)