# Openpilot mapd
Provides openpilot with data from mapd

## Using
### Download Maps
Maps can be downloaded in one of two ways, by arbitrary bounding box or by
pre-defined locations.

#### Download by Bounding Box
To download an arbitrary bounding box write the bounding box to
/dev/shm/params/d/OSMDownloadBounds using the following json format:
```json
{
    "min_lon": -84.82069386553866,
    "min_lat": 38.40504468653686,
    "max_lon": -80.52071966199662,
    "max_lat": 41.97787393972939
}
```

When mapd downloads the specified bounding box it will clip the given bounding
box to regions of 2x2 degrees due to how the files are stored in the cloud. So
for the previous example the actual downloaded region would be:
```json
{
    "min_lon": -86,
    "min_lat": 38,
    "max_lon": -80,
    "max_lat": 42
}
```
#### Download by Pre-defined Locations
mapd contains pre-defined bounding boxes for ISO 3166-1 nations and US
states/territories. The nation bounding boxes are stored in
[nation_bounding_boxes.json](./nation_bounding_boxes.json) and the US
states/territories bounding boxes are stored in
[us_states_bounding_boxes.json](./us_states_bounding_boxes.json).

To download by nation you must write a json object containing a list of
abbreviations from the nation_bounding_boxes.json file to
/dev/shm/params/d/OSMDownloadLocations using the following format:
```json
{
    "nations": ["US", "CA"],
    "states": []
}
```
To download by US states/territories you must write a json object containing a
list of abbreviations from the states_bounding_boxes.json file to
/dev/shm/params/d/OSMDownloadLocations using the following format:
```json
{
    "nations": [],
    "states": ["OH", "KY", "WV"]
}
```

Note that when downloading the locations the bounding boxes specified in the
respective json files will actually be clipped to regions of 2x2 degrees the
same as when specifying an arbitrary bounding box to download. Also, when the
region completely surrounds another smaller region the smaller region will not
be excluded from the download.


## Build
### Build capnp files
```bash
capnp compile -I ../go-capnp/std -ogo offline.capnp
```

### Build Release Binary
```bash
CGO_ENABLED=0 go build -ldflags="-extldflags=-static -s -w"
```

NOTE: Must be built for ARM64 to be used on device
