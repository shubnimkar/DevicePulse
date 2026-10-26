// slim_telemetry shrinks telemetry documents that were stored in full before the
// API started storing them slim.
//
//	go run ./cmd/slim_telemetry --dry-run     report what would change; writes nothing
//	go run ./cmd/slim_telemetry --apply       do it
//
// A "full" document carries every collector's output (services, installed apps,
// process list, ports, ...), about 31 KB. Nothing in the dashboard reads those
// copies: current values come from the devices collection, and the telemetry
// collection is only used for timestamps (connection history), per-app focus
// summaries and the browser-history archive pointer. For each device and
// collector role the first full document in every --snapshot-minutes window is
// kept as a snapshot; the rest lose the heavy fields. Documents that are already
// slim, snapshots, or archived to S3 are left alone, so re-running is safe.
//
// Space freed inside MongoDB is reused by new writes, but the data files only
// shrink after the cluster compacts (Atlas does this on resize/maintenance).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const dbName = "devicepulse"

// heavyFields are removed from slimmed documents. Kept: identity and timestamps,
// data.ActiveWindowTracker.app_summaries (focus totals) and the
// data.BrowserHistory.archive pointer.
var heavyFields = []string{
	"data.SystemInfo", "data.ProcessMonitor", "data.HardwareStats", "data.Services",
	"data.NetworkPorts", "data.USBEvents", "data.InstalledApps", "data.OSUpdates",
	"data.ActiveWindowTracker.sessions", "data.ActiveWindowTracker.cumulative_summaries",
	"data.BrowserHistory.new_history_entries", "data.BrowserHistory.top_recent_urls",
}

// slimDocBytes is the typical size of a slim document, used only for the estimate.
const slimDocBytes = 1024

type options_ struct {
	Apply           bool
	SnapshotMinutes int
	Batch           int
	Limit           int64
}

type result struct {
	Scanned, AlreadySlim, Archived, Snapshots, Kept, Slimmed int64
	BytesBefore                                              int64
}

func main() {
	dryRun := flag.Bool("dry-run", false, "report only; change nothing")
	apply := flag.Bool("apply", false, "slim the documents")
	snapshot := flag.Int("snapshot-minutes", 60, "keep the first full document per device and role in each window of this many minutes (0 keeps none)")
	batch := flag.Int("batch", 1000, "documents per update")
	limit := flag.Int64("limit", 0, "slim at most this many documents (0 = all)")
	flag.Parse()
	if *dryRun == *apply {
		log.Fatal("specify exactly one of --dry-run or --apply")
	}
	_ = godotenv.Load(".env")
	uri := strings.TrimSpace(os.Getenv("MONGO_URI"))
	if uri == "" {
		log.Fatal("MONGO_URI is required")
	}

	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri).SetServerSelectionTimeout(20*time.Second))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Disconnect(ctx)
	if err := client.Ping(ctx, nil); err != nil {
		log.Fatal(err)
	}

	res, err := run(ctx, client.Database(dbName).Collection("telemetry"), options_{
		Apply: *apply, SnapshotMinutes: *snapshot, Batch: *batch, Limit: *limit,
	})
	if err != nil {
		log.Fatal(err)
	}
	mode := "DRY RUN (nothing changed)"
	if *apply {
		mode = "APPLIED"
	}
	estimate := res.BytesBefore - res.Slimmed*slimDocBytes
	fmt.Printf("%s\n  scanned %d documents\n  already slim: %d, archived to S3: %d, snapshots: %d\n  full documents kept as snapshots: %d\n  slimmed: %d (%.0f MB now, about %.0f MB saved)\n",
		mode, res.Scanned, res.AlreadySlim, res.Archived, res.Snapshots, res.Kept, res.Slimmed,
		float64(res.BytesBefore)/1048576, float64(estimate)/1048576)
	if !*apply {
		fmt.Println("Run again with --apply to slim these documents.")
	}
}

func bucket(t time.Time, minutes int) int64 {
	if minutes <= 0 {
		return 0
	}
	return t.Unix() / int64(minutes*60)
}

// run walks the collection once, oldest first, deciding per document whether to
// keep it whole or slim it. It writes only when opts.Apply is set.
func run(ctx context.Context, coll *mongo.Collection, opts options_) (result, error) {
	var res result
	cur, err := coll.Find(ctx, bson.M{}, options.Find().
		SetSort(bson.D{{Key: "_id", Value: 1}}).
		SetProjection(bson.M{"device_id": 1, "collector_role": 1, "ingested_at": 1, "snapshot": 1, "slim": 1, "telemetry_archive": 1}).
		SetBatchSize(1000))
	if err != nil {
		return res, err
	}
	defer cur.Close(ctx)

	seen := map[string]bool{} // device|role|window already holds a full document
	var pending []primitive.ObjectID

	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		ids := pending
		pending = nil
		// Size of what is about to be slimmed, for the report.
		agg, err := coll.Aggregate(ctx, mongo.Pipeline{
			{{Key: "$match", Value: bson.M{"_id": bson.M{"$in": ids}}}},
			{{Key: "$group", Value: bson.M{"_id": nil, "bytes": bson.M{"$sum": bson.M{"$bsonSize": "$$ROOT"}}}}},
		})
		if err != nil {
			return err
		}
		var rows []struct {
			Bytes int64 `bson:"bytes"`
		}
		if err := agg.All(ctx, &rows); err != nil {
			return err
		}
		if len(rows) > 0 {
			res.BytesBefore += rows[0].Bytes
		}
		res.Slimmed += int64(len(ids))
		if !opts.Apply {
			return nil
		}
		unset := bson.M{}
		for _, f := range heavyFields {
			unset[f] = ""
		}
		_, err = coll.UpdateMany(ctx, bson.M{"_id": bson.M{"$in": ids}},
			bson.M{"$unset": unset, "$set": bson.M{"slim": true}})
		return err
	}

	for cur.Next(ctx) {
		var d struct {
			ID         primitive.ObjectID `bson:"_id"`
			DeviceID   string             `bson:"device_id"`
			Role       string             `bson:"collector_role"`
			IngestedAt time.Time          `bson:"ingested_at"`
			Snapshot   bool               `bson:"snapshot"`
			Slim       bool               `bson:"slim"`
			Archive    interface{}        `bson:"telemetry_archive"`
		}
		if err := cur.Decode(&d); err != nil {
			return res, err
		}
		res.Scanned++
		key := fmt.Sprintf("%s|%s|%d", d.DeviceID, d.Role, bucket(d.IngestedAt, opts.SnapshotMinutes))
		switch {
		case d.Slim:
			res.AlreadySlim++
		case d.Archive != nil:
			res.Archived++
		case d.Snapshot:
			res.Snapshots++
			seen[key] = true
		case opts.SnapshotMinutes > 0 && !seen[key]:
			seen[key] = true // first full document in its window stays whole
			res.Kept++
		default:
			if opts.Limit > 0 && res.Slimmed+int64(len(pending)) >= opts.Limit {
				continue
			}
			pending = append(pending, d.ID)
			if len(pending) >= opts.Batch {
				if err := flush(); err != nil {
					return res, err
				}
			}
		}
	}
	if err := cur.Err(); err != nil {
		return res, err
	}
	return res, flush()
}
