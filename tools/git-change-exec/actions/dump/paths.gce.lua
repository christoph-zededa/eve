paths = {}

function match(lf, ld)
	if paths[lf:Path()] ~= nil then
		return false
	end
	paths[lf:Path()] = true
	return true
end

function exec()
	for k in pairs(paths) do
		print("path: " .. k)
	end
	return true
end
