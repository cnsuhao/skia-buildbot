// kmlabel enables k-means clustering on params.
package kmlabel

/*
Allow k-means clustering on trace params by mapping them to unit vectors in an
n+1 dimensional space. We use n+1 dimensions and map the absence of a value for
a given param as the vector <1, 0, ...> That is, if we have the following
paramset (note the param values are sorted):

	paramset := map[string][]string{
		"config": []string{"565", "8888"},
		"test":   []string{"a", "b", "c", "d"},
	}

Then the following params:

	traceparams := map[string]map[string]string{
		"tr1": map[string]string{
			"config": "8888",
			"test":    "a",
		},
		"tr2": map[string]string{
			"config": "565",
			"test":    "b",
		},
		"tr4": map[string]string{
			"config": "8888",
		},
	}

Let's presume a paramset of

	paramset := map[string][]string{
		"config": []string{"8888", "565"},
		"test":   []string{"a", "b", "c", "d"},
	}

Then the traces would be mapped to unit vectors as:

  //        config      test
  tr1 ->  <0, 0, 1> <0, 1, 0, 0>
  tr2 ->  <0, 1, 0> <0, 0, 1, 0>
  tr3 ->  <0, 0, 1> <1, 0, 0, 0>

Since these are always unit vectors we simplify their representation
by recording the location of the only '1'.


And we can sort the param names in the paramset, keeping the param
order fixed, so our representation becomes:

  //      config, test
  tr1 -> []int{2, 1}
  tr2 -> []int{1, 2}
  tr3 -> []int{2, 0}


This compressed representation is what's used in the implementation below.

Note that each param has its own set of unit vectors, and therefore the params
in a trace will map to a set of unit vectors. Also note that we need to know
the paramset for all the traces so we know all possible values that would
appear for a param.

*/

import (
	"math"
	"math/rand"
	"sort"
	"time"

	"go.skia.org/infra/perf/go/kmeans"
	"go.skia.org/infra/perf/go/types"
	"go.skia.org/infra/perf/go/valueweight"
)

func init() {
	rand.Seed(time.Now().Unix())
}

// Trace implements kmeans.Clusterable.
type Trace struct {
	ID     string
	Params []int
}

// Measure implements the mapping of categorical data into a Euclidean space.
// I.e. for config=8888,565 it would map 565 to <1, 0> and 8888 to <0, 1>.
// Since these are all unit basis vectors we can just store them as offsets in
// Counts. Actually this would map:
//
//   nil     ->  <1, 0, 0>
//   "565"   ->  <0, 1, 0>
//   "8888"  ->  <0, 0, 1>
//
// since the way Centroid uses Measure is to reserve the 0th spot for the case
// where a trace doesn't have any value for a key, e.g. the name of the GPU for
// a CPU test.
//
// For the rest of the documentation on Measure we will assume it is for
// a config=8888,565 and was initialized with a size of 3, with the 0th
// spot for a trace not having any value for 'config'.
type Measure struct {
	// Counts is the number of times each value in a key, value pair has been seen.
	Counts []int

	// Distances is the pre-calculated distance to each value, based on Counts.
	Distances []float64

	// squares just used in Calc, but adding it here avoids allocating a new one
	// each time Calc is called.
	squares []float64
}

// NewMeasure returns a new Measure for a parameter with 'size' possible values.
func NewMeasure(size int) *Measure {
	return &Measure{
		Counts:    make([]int, size),
		Distances: make([]float64, size),
		squares:   make([]float64, size),
	}
}

// Inc adds a new value to be used when calculating the Distance.
//
// For example '8888' maps to <0, 0, 1>, which would make a call to Inc(2),
// If a trace didn't have a value for 'config' then this would call Inc(0).
func (m *Measure) Inc(i int) {
	m.Counts[i] += 1
}

// Clear resets all the counts to 0.
func (m *Measure) Clear() {
	for i, _ := range m.Counts {
		m.Counts[i] = 0.0
	}
}

// Distance returns the distance from the centroid to param value i.
func (m *Measure) Distance(i int) float64 {
	return m.Distances[i]
}

// Calc precalculates the distance metrics for all possible param values.
//
// Doing a little bit of algebra up front to simplify the calculations.
// For each param (i) we want to calculate:
//
//   m.Distances[i] = ||ith vector - center||
//
// Where the center is the average of the m.Counts vectors.
//
// To simplify notation we will denote
//
//   m.Counts[0], m.Counts[1], ...
//
// As
//
//   c_0, c_1, ...
//
// m.Counts really represents the sum of the vectors, all of length len(m.Counts)
// The center vector has a value of:
//
//   <c_0/sum, c_1/sum, ...>
//
// Each basis vector has a value of
//
//   B_j = <0, 0, 1, 0, ...>    i.e. a 1 in the jth spot.
//
// The distance between B_j and the center vector is:
//
//    c_i/sum - 0                                         for i <> j
//    c_i/sum - 1 = c_i/sum - sum/sum = (c_i - sum)/sum   for i == j
//
// Since everything is divided by sum^2 we can factor that out when
// calculating the Euclidean distance:
//
//    m.Distances[i] = sqrt(c_0^2 + c_1^2 + ... + (c_1 - sum)^2 + ... + c_size^2)/sum
//
// If we let:
//
//   tss = (c_0^2 + c_1^2 +...+ c_size^2),   i.e. sum of all squares.
//
// Then the equation becomes:
//
//   m.Distances[i] = sqrt(tss - c_i^2  + (sum-c_i)^2)/sum
//
func (m *Measure) Calc() {
	sum := 0.0
	for i, _ := range m.Counts {
		sum += float64(m.Counts[i])
	}
	tss := 0.0
	for i, x := range m.Counts {
		m.squares[i] = float64(x) * float64(x)
		tss += m.squares[i]
	}
	for i, _ := range m.Counts {
		delta := sum - float64(m.Counts[i])
		m.Distances[i] = math.Sqrt(tss-m.squares[i]+delta*delta) / sum
	}
}

// Centroid implments kmeans.Centroid for params.
type Centroid struct {
	// One Measure per key in the params.
	Dimensions []*Measure
}

// NewCentroid returns a new Centroid for a paramset where the number of param values
// for each param is given in 'dimSizes' and an initial vector for the centroid is
// given in 'initial'. If 'initial' is nil then there is no initial vector.
//
// For example, if the paramset was
//	paramset := map[string][]string{
//		"config": []string{"8888", "565"},
//		"test":   []string{"a", "b", "c", "d"},
//	}
//
// Note that param names and values are sorted before assigning vector locations.
//
//   dimSizes = []int{3, 5}
//
// and an initial value for the centroid of config=8888, test=c would be:
//
//   initial = []int{2, 3}
//
// which takes sort order into account. I.e 'config' sorts before 'test',
// and 8888 = <0, 0, 1, 0> and 'c' = <0, 0, 0, 1, 0>.
func NewCentroid(dimSizes []int, initial []int) *Centroid {
	c := &Centroid{
		Dimensions: make([]*Measure, len(dimSizes)),
	}
	for i, size := range dimSizes {
		m := NewMeasure(size)
		if initial != nil {
			m.Inc(initial[i])
		}
		m.Calc()
		c.Dimensions[i] = m
	}
	return c
}

func (c *Centroid) AsClusterable() kmeans.Clusterable {
	// kmlabel Centroids can't be turned back into Clusterables.
	return nil
}

// Distance calculates the distance from this Centroid to a given
// kmeans.Clusterable.
//
// Calculates simple Euclidean distance among the Measures.
func (c *Centroid) Distance(clusterable kmeans.Clusterable) float64 {
	total := 0.0
	t := clusterable.(*Trace)
	for i, p := range t.Params {
		d := c.Dimensions[i].Distance(p)
		total += d * d
	}
	return math.Sqrt(total)
}

// Clear resets all the counts for all the Dimensions.
func (c *Centroid) Clear() {
	for _, m := range c.Dimensions {
		m.Clear()
	}
}

// Add a trace that makes up the calculation of the centroid.
func (c *Centroid) Add(t *Trace) {
	for i, m := range c.Dimensions {
		m.Inc(t.Params[i])
	}
}

// Finished calculates the centroid from all the added traces.
func (c *Centroid) Finished() {
	for _, m := range c.Dimensions {
		m.Calc()
	}
}

// Reverse is a type of func that converts a Trace back into params.
type Reverse func(t *Trace) map[string]string

// CentroidsAndTraces returns a set of starting Centroids and a set of Traces
// from the given paramset and a map of trace ids to params for the trace.
//
// It also returns a closure that implements kmeans.CalculateCentroid and a closure
// that implements Reverse.
func CentroidsAndTraces(paramset map[string][]string, traceparams map[string]map[string]string, K int) ([]kmeans.Centroid, []kmeans.Clusterable, kmeans.CalculateCentroid, Reverse) {
	// Sort the paramset keys.
	keys := []string{}
	for k, _ := range paramset {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	size := len(keys)

	// map[paramname] ->  map[paramvalue] -> int (offset of value + 1)
	paramMap := map[string]map[string]int{}
	// map[paramname] -> int offset of param name in keys.
	paramIndex := map[string]int{}
	// dimSizes if the number of param values (+1) for each param key.
	dimSizes := make([]int, size)
	// sortedParamset is the paramset, but with sorted values.
	sortedParamset := map[string][]string{}
	for i, key := range keys {
		paramIndex[key] = i
		params := paramset[key]
		dimSizes[i] = len(params) + 1 // Make room for 0th value.
		sort.Strings(params)
		sortedParamset[key] = params
		paramIndices := map[string]int{}
		for i, p := range params {
			paramIndices[p] = i + 1
		}
		paramMap[key] = paramIndices
	}

	// reverse implements Reverse.
	reverse := func(t *Trace) map[string]string {
		ret := map[string]string{}
		for i, p := range t.Params {
			if p == 0 {
				continue
			}
			key := keys[i]
			value := sortedParamset[key][p-1]
			ret[key] = value
		}
		return ret
	}

	traces := make([]kmeans.Clusterable, 0, len(traceparams))
	for id, params := range traceparams {
		tr := &Trace{
			ID:     id,
			Params: make([]int, size),
		}
		for k, v := range params {
			// Note that if a trace doesn't contain a value for a param key then it
			// defaults to 0, which is what we want.
			tr.Params[paramIndex[k]] = paramMap[k][v]
		}
		traces = append(traces, tr)
	}

	// Create the centroids from a random subset of traces.
	centroids := make([]kmeans.Centroid, K)
	for i, _ := range centroids {
		// Pick a trace at random.
		tr := traces[rand.Intn(len(traces))].(*Trace)
		centroids[i] = NewCentroid(dimSizes, tr.Params)
	}

	// f implements kmeans.CalculateCentroid.
	f := func(traces []kmeans.Clusterable) kmeans.Centroid {
		centroid := NewCentroid(dimSizes, nil)
		for _, t := range traces {
			centroid.Add(t.(*Trace))
		}
		centroid.Finished()
		return centroid
	}

	return centroids, traces, f, reverse
}

// Description is returned by ClusterAndDescribe.
type Description struct {
	// Centers describes each k-means generated cluster.
	Centers []*Center `json:"centers"`

	// Percent is the percentage of traces we analyzed.
	Percent float64 `json:"percent"`
}

// Center describes a single cluster of traces.
type Center struct {
	// IDs is a slice of <=20 IDs of traces, the closest 20 to the centroid of
	// the cluster.
	IDs []string `json:"ids"`

	// WordCloud is a word cloud of all the parameter values that appear in the
	// cluster.
	WordCloud [][]types.ValueWeight `json:"wordcloud"`

	// Size is the number of traces in this cluster.
	Size int `json:"size"`
}

// ClusterAndDescribe takes all the params from a set of traces, as passed in
// via traceparams, and does k-means clustering on the parameters and returns
// the results of the clustering in a Description.
//
// The paramset is needed so we know the total number of values for each
// parameter.  The value of total is the total number of traces being analyzed,
// of which traceparams contains a subset.
func ClusterAndDescribe(paramSet map[string][]string, traceparams map[string]map[string]string, total int) Description {
	if len(traceparams) == 0 {
		return Description{
			Centers: []*Center{},
			Percent: 0,
		}
	}

	// A good guess for k is sqrt(n)/2.
	k := int(math.Sqrt(float64(len(traceparams))) / 2.0)
	// But never go below 5 clusters.
	if k < 5 {
		k = 5
	}
	cent, obs, f, reverse := CentroidsAndTraces(paramSet, traceparams, k)
	_, clusters := kmeans.KMeans(obs, cent, k, 100, f)

	// Now that clustering is complete build of the slice of Center's for each
	// cluster found.
	centers := []*Center{}
	for _, cl := range clusters {
		size := len(cl)
		params := []map[string]string{}
		for _, tr := range cl {
			params = append(params, reverse(tr.(*Trace)))
		}
		if len(cl) > 20 {
			cl = cl[:20]
		}
		ids := make([]string, len(cl))
		for i, tr := range cl {
			ids[i] = tr.(*Trace).ID
		}
		centers = append(centers, &Center{
			IDs:       ids,
			WordCloud: valueweight.FromParams(params),
			Size:      size,
		})
	}

	return Description{
		Centers: centers,
		Percent: float64(len(traceparams)) / float64(total),
	}
}
