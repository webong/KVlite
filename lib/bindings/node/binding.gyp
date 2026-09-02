{
  "targets": [
    {
      "target_name": "kvlite_native",
      "sources": ["native/kvlite_native.c"],
      "defines": ["NAPI_VERSION=8"],
      "conditions": [
        ["OS=='linux'", {"libraries": ["-ldl"]}]
      ]
    }
  ]
}
