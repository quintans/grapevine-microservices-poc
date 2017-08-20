// register the grid component
var grid = Vue.component('demo-grid', {
  template: '#grid-template',
  props: {
	sortColumn: String,
    data: Array,
    columns: Array,
    filterKey: String
  },
  data: function () {
    var sortOrders = {}
    this.columns.forEach(function (key) {
      sortOrders[key] = 1
    })
    return {
      sortKey: '',
      sortOrders: sortOrders
    }
  },
  computed: {
    filteredData: function () {
      var sortKey = this.sortKey
      var filterKey = this.filterKey && this.filterKey.toLowerCase()
      var order = this.sortOrders[sortKey] || 1
      var data = this.data
      if (filterKey) {
        data = data.filter(function (row) {
          return Object.keys(row).some(function (key) {
            return String(row[key]).toLowerCase().indexOf(filterKey) > -1
          })
        })
      }
      if (sortKey) {
        data = data.slice().sort(function (a, b) {
          a = a[sortKey]
          b = b[sortKey]
          return (a === b ? 0 : a > b ? 1 : -1) * order
        })
      }
      return data
    }
  },
  filters: {
    capitalize: function (str) {
      return str.charAt(0).toUpperCase() + str.slice(1)
    }
  },
  methods: {
    sortBy: function (key) {
      this.sortKey = key
      this.sortOrders[key] = this.sortOrders[key] * -1
    }
  }
})

// bootstrap the demo
var demo = new Vue({
  el: '#demo',
  data: {
	cbGrid: {
	    searchQuery: '',
	    gridColumns: ['Name', 'State', 'Success', 'Rate', 'Nodes', 'Type'],
	    gridData: []
	},
	lbGrid: {
	    searchQuery: '',
	    gridColumns: ['Name', 'Quarantine', 'Success', 'Rate', 'Location', 'Type'],
	    gridData: []
	}
  }
})

function ratio(ok, nok) {
	var r = ok + nok
	if (r !== 0) {
	 	r = ok/r*100
	}
	return '('+ok+'/'+nok+') '+r.toFixed()+'%'
}

var result = document.getElementById("result")
var evtSource = new EventSource("/stats");

evtSource.onmessage = function(e) {
	var data = JSON.parse(e.data);

	data.Cb.forEach(function (x) {
      switch (x.State) {
		case 0:
			x.State = 'CLOSE'
			break;
		case 1:
			x.State = 'OPEN'
			break;
		case 2:
			x.State = 'HALFOPEN'
			break;
		}

		x.Rate = (x.Successes / x.Period).toFixed() + '/s'
		x.Success = ratio(x.Successes, x.Fails)
		// Nodes
		var all = 0
		var ok = 0
		data.Lb.forEach(function (y) {
			if (y.Name === x.Name) {
				all++
				if (!y.Quarantine) {
					ok++
				}
			}
		})
		x.Nodes = ok + '/' + all
    })
	demo.cbGrid.gridData = data.Cb

	data.Lb.forEach(function (x) {
		x.Success = ratio(x.Successes, x.Fails)
		x.Rate = (x.Successes / x.Period).toFixed() + '/s'
	})
	demo.lbGrid.gridData = data.Lb
}
