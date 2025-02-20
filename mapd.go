package main

import (
	"encoding/json"
	"flag"
	"os"
	"time"

	"capnproto.org/go/capnp/v3"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
)

type State struct {
	Data          []uint8
	CurrentWay    CurrentWay
	NextWay       NextWayResult
	SecondNextWay NextWayResult
	Position      Position
}

type Position struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Bearing   float64 `json:"bearing"`
}

type NextSpeedLimit struct {
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	Speedlimit float64 `json:"speedlimit"`
}

type AdvisoryLimit struct {
	StartLatitude  float64 `json:"start_latitude"`
	StartLongitude float64 `json:"start_longitude"`
	EndLatitude    float64 `json:"end_latitude"`
	EndLongitude   float64 `json:"end_longitude"`
	Speedlimit     float64 `json:"speedlimit"`
}

type Hazard struct {
	StartLatitude  float64 `json:"start_latitude"`
	StartLongitude float64 `json:"start_longitude"`
	EndLatitude    float64 `json:"end_latitude"`
	EndLongitude   float64 `json:"end_longitude"`
	Hazard         string  `json:"hazard"`
}

func RoadName(way Way) string {
	name, err := way.Name()
	if err == nil {
		if len(name) > 0 {
			return name
		}
	}
	ref, err := way.Ref()
	if err == nil {
		if len(ref) > 0 {
			return ref
		}
	}
	return ""
}

func readOffline(data []uint8) Offline {
	msg, err := capnp.UnmarshalPacked(data)
	logde(errors.Wrap(err, "could not unmarshal offline data"))
	if err == nil {
		offline, err := ReadRootOffline(msg)
		logde(errors.Wrap(err, "could not read offline message"))
		return offline
	}
	return Offline{}
}

func readPosition(persistent bool) (Position, error) {
	path := LAST_GPS_POSITION
	if persistent {
		path = LAST_GPS_POSITION_PERSIST
	}

	pos := Position{}
	coordinates, err := GetParam(path)
	if err != nil {
		return pos, errors.Wrap(err, "could not read coordinates param")
	}
	err = json.Unmarshal(coordinates, &pos)
	return pos, errors.Wrap(err, "could not unmarshal coordinates")
}

func loop(state *State) {
	defer func() {
		if err := recover(); err != nil {
			e := errors.Errorf("panic occured: %v", err)
			loge(e)
			// reset state for next loop
			state.Data = []uint8{}
			state.NextWay = NextWayResult{}
			state.CurrentWay = CurrentWay{}
			state.Position = Position{}
			state.SecondNextWay = NextWayResult{}
		}
	}()

	logLevelData, err := GetParam(MAPD_LOG_LEVEL)
	if err == nil {
		level, err := zerolog.ParseLevel(string(logLevelData))
		if err == nil {
			zerolog.SetGlobalLevel(level)
		}
	}
	prettyLog, err := GetParam(MAPD_PRETTY_LOG)
	if err == nil && len(prettyLog) > 0 && prettyLog[0] == '1' {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
		logde(RemoveParam(MAPD_PRETTY_LOG))
	} else if err == nil && len(prettyLog) > 0 && prettyLog[0] == '0' {
		log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
		logde(RemoveParam(MAPD_PRETTY_LOG))
	}

	target_lat_a, err := GetParam(MAP_TARGET_LAT_A)
	if err == nil && len(target_lat_a) > 0 {
		var t_lat_a float64
		err = json.Unmarshal(target_lat_a, &t_lat_a)
		if err == nil {
			TARGET_LAT_ACCEL = t_lat_a
			_ = RemoveParam(MAP_TARGET_LAT_A)
			log.Info().Float64("target_lat_accel", t_lat_a).Msg("loaded memory target lateral accel")
		}
	}

	time.Sleep(1 * time.Second)
	DownloadIfTriggered()

	pos, err := readPosition(false)
	if err != nil {
		logwe(errors.Wrap(err, "could not read current position"))
		return
	}
	offline := readOffline(state.Data)

	// ------------- Find current and next ways ------------

	if !PointInBox(pos.Latitude, pos.Longitude, offline.MinLat(), offline.MinLon(), offline.MaxLat(), offline.MaxLon()) {
		state.Data, err = FindWaysAroundLocation(pos.Latitude, pos.Longitude)
		logde(errors.Wrap(err, "could not find ways around current location"))
	}

	state.CurrentWay, err = GetCurrentWay(state.CurrentWay.Way, state.NextWay.Way, state.SecondNextWay.Way, offline, pos)
	logde(errors.Wrap(err, "could not get current way"))

	state.NextWay, err = NextWay(state.CurrentWay.Way, offline, state.CurrentWay.OnWay.IsForward)
	logde(errors.Wrap(err, "could not get next way"))

	state.SecondNextWay, err = NextWay(state.NextWay.Way, offline, state.NextWay.IsForward)
	logde(errors.Wrap(err, "could not get second next way"))

	curvatures, err := GetStateCurvatures(state)
	logde(errors.Wrap(err, "could not get curvatures from current state"))
	target_velocities := GetTargetVelocities(curvatures)

	// -----------------  Write data ---------------------

	// -----------------  MTSC Data  -----------------------
	data, err := json.Marshal(curvatures)
	logde(errors.Wrap(err, "could not marshal curvatures"))
	err = PutParam(MAP_CURVATURES, data)
	logwe(errors.Wrap(err, "could not write curvatures"))

	data, err = json.Marshal(target_velocities)
	logde(errors.Wrap(err, "could not marshal target velocities"))
	err = PutParam(MAP_TARGET_VELOCITIES, data)
	logwe(errors.Wrap(err, "could not write curvatures"))

	// ----------------- Current Data --------------------
	err = PutParam(ROAD_NAME, []byte(RoadName(state.CurrentWay.Way)))
	logwe(errors.Wrap(err, "could not write road name"))

	data, err = json.Marshal(state.CurrentWay.Way.MaxSpeed())
	logde(errors.Wrap(err, "could not marshal speed limit"))
	err = PutParam(MAP_SPEED_LIMIT, data)
	logwe(errors.Wrap(err, "could not write speed limit"))

	data, err = json.Marshal(state.CurrentWay.Way.AdvisorySpeed())
	logde(errors.Wrap(err, "could not marshal advisory speed limit"))
	err = PutParam(MAP_ADVISORY_LIMIT, data)
	logwe(errors.Wrap(err, "could not write advisory speed limit"))

	hazard, err := state.CurrentWay.Way.Hazard()
	logde(errors.Wrap(err, "could not read current way hazard"))
	data, err = json.Marshal(Hazard{
		StartLatitude:  state.CurrentWay.StartPosition.Latitude(),
		StartLongitude: state.CurrentWay.StartPosition.Longitude(),
		EndLatitude:    state.CurrentWay.EndPosition.Latitude(),
		EndLongitude:   state.CurrentWay.EndPosition.Longitude(),
		Hazard:         hazard,
	})
	logde(errors.Wrap(err, "could not marshal hazard"))
	err = PutParam(MAP_HAZARD, data)
	logwe(errors.Wrap(err, "could not write hazard"))

	data, err = json.Marshal(AdvisoryLimit{
		StartLatitude:  state.CurrentWay.StartPosition.Latitude(),
		StartLongitude: state.CurrentWay.StartPosition.Longitude(),
		EndLatitude:    state.CurrentWay.EndPosition.Latitude(),
		EndLongitude:   state.CurrentWay.EndPosition.Longitude(),
		Speedlimit:     state.CurrentWay.Way.AdvisorySpeed(),
	})
	logde(errors.Wrap(err, "could not marshal advisory speed limit"))
	err = PutParam(MAP_ADVISORY_LIMIT, data)
	logwe(errors.Wrap(err, "could not write advisory speed limit"))

	// ---------------- Next Data ---------------------

	hazard, err = state.NextWay.Way.Hazard()
	logde(errors.Wrap(err, "could not read next hazard"))
	data, err = json.Marshal(Hazard{
		StartLatitude:  state.NextWay.StartPosition.Latitude(),
		StartLongitude: state.NextWay.StartPosition.Longitude(),
		EndLatitude:    state.NextWay.EndPosition.Latitude(),
		EndLongitude:   state.NextWay.EndPosition.Longitude(),
		Hazard:         hazard,
	})
	logde(errors.Wrap(err, "could not marshal next hazard"))
	err = PutParam(NEXT_MAP_HAZARD, data)
	logwe(errors.Wrap(err, "could not write next hazard"))

	currentMaxSpeed := state.CurrentWay.Way.MaxSpeed()
	nextMaxSpeed := state.NextWay.Way.MaxSpeed()
	secondNextMaxSpeed := state.SecondNextWay.Way.MaxSpeed()
	var nextSpeedWay NextWayResult
	if (nextMaxSpeed != currentMaxSpeed || secondNextMaxSpeed == currentMaxSpeed) && (nextMaxSpeed != 0 || secondNextMaxSpeed == 0) {
		nextSpeedWay = state.NextWay
	} else {
		nextSpeedWay = state.SecondNextWay
	}
	data, err = json.Marshal(NextSpeedLimit{
		Latitude:   nextSpeedWay.StartPosition.Latitude(),
		Longitude:  nextSpeedWay.StartPosition.Longitude(),
		Speedlimit: nextSpeedWay.Way.MaxSpeed(),
	})
	logde(errors.Wrap(err, "could not marshal next speed limit"))
	err = PutParam(NEXT_MAP_SPEED_LIMIT, data)
	logwe(errors.Wrap(err, "could not write next speed limit"))

	currentAdvisorySpeed := state.CurrentWay.Way.AdvisorySpeed()
	nextAdvisorySpeed := state.NextWay.Way.AdvisorySpeed()
	secondNextAdvisorySpeed := state.SecondNextWay.Way.AdvisorySpeed()
	var nextAdvisoryWay NextWayResult
	if (nextAdvisorySpeed != currentAdvisorySpeed || secondNextAdvisorySpeed == currentAdvisorySpeed) && (nextAdvisorySpeed != 0 || secondNextAdvisorySpeed == 0) {
		nextAdvisoryWay = state.NextWay
	} else {
		nextAdvisoryWay = state.SecondNextWay
	}
	data, err = json.Marshal(AdvisoryLimit{
		StartLatitude:  nextAdvisoryWay.StartPosition.Latitude(),
		StartLongitude: nextAdvisoryWay.StartPosition.Longitude(),
		EndLatitude:    nextAdvisoryWay.EndPosition.Latitude(),
		EndLongitude:   nextAdvisoryWay.EndPosition.Longitude(),
		Speedlimit:     nextAdvisoryWay.Way.AdvisorySpeed(),
	})
	logde(errors.Wrap(err, "could not marshal next advisory speed limit"))
	err = PutParam(NEXT_MAP_ADVISORY_LIMIT, data)
	logwe(errors.Wrap(err, "could not write next advisory speed limit"))
}

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixNano
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack
	l := zerolog.InfoLevel
	logLevelData, err := GetParam(MAPD_LOG_LEVEL_PERSIST)
	if err == nil {
		level, err := zerolog.ParseLevel(string(logLevelData))
		if err == nil {
			l = level
		}
	}
	zerolog.SetGlobalLevel(l)
	prettyLog, err := GetParam(MAPD_PRETTY_LOG_PERSIST)
	if err == nil && len(prettyLog) > 0 && prettyLog[0] == '1' {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	generatePtr := flag.Bool("generate", false, "Triggers a generation of map data from 'map.osm.pbf'")
	minGenLatPtr := flag.Int("minlat", -90, "the minimum latitude to generate")
	minGenLonPtr := flag.Int("minlon", -180, "the minimum longitude to generate")
	maxGenLatPtr := flag.Int("maxlat", -90, "the maximum latitude to generate")
	maxGenLonPtr := flag.Int("maxlon", -180, "the maximum longitude to generate")
	flag.Parse()
	if *generatePtr {
		GenerateOffline(*minGenLatPtr, *minGenLonPtr, *maxGenLatPtr, *maxGenLonPtr)
		return
	}
	EnsureParamDirectories()
	ResetParams()
	state := State{}

	pos, err := readPosition(true)
	logde(err)
	if err == nil {
		state.Data, err = FindWaysAroundLocation(pos.Latitude, pos.Longitude)
		logde(errors.Wrap(err, "could not find ways around initial location"))
	}

	target_lat_a, err := GetParam(MAP_TARGET_LAT_A_PERSIST)
	if err == nil && len(target_lat_a) > 0 {
		var t_lat_a float64
		err = json.Unmarshal(target_lat_a, &t_lat_a)
		if err == nil {
			TARGET_LAT_ACCEL = t_lat_a
			log.Info().Float64("target_lat_accel", t_lat_a).Msg("loaded persistent target lateral accel")
		}
	}

	for {
		loop(&state)
	}
}
