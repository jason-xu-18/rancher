package store

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/rancher/types/config"

	"github.com/rancher/norman/types/values"

	"github.com/rancher/norman/types/convert"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/norman/types"
	"github.com/rancher/types/apis/core/v1"
	managementschema "github.com/rancher/types/apis/management.cattle.io/v3/schema"
	projectschema "github.com/rancher/types/apis/project.cattle.io/v3/schema"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var separator = ".."

type PasswordStore struct {
	Schemas     map[string]*types.Schema
	Fields      map[string]map[string]interface{}
	Stores      map[string]types.Store
	secretStore v1.SecretInterface
	nsStore     v1.NamespaceInterface
}

func SetPasswordStore(schemas *types.Schemas, secretStore v1.SecretInterface, nsStore v1.NamespaceInterface) {
	modifyProjectTypes := map[string]bool{
		"githubPipelineConfig": true,
		"gitlabPipelineConfig": true,
	}

	pwdStore := &PasswordStore{
		Schemas:     map[string]*types.Schema{},
		Fields:      map[string]map[string]interface{}{},
		Stores:      map[string]types.Store{},
		secretStore: secretStore,
		nsStore:     nsStore,
	}

	pwdTypes := []string{}

	for _, storeType := range pwdTypes {
		var schema *types.Schema
		if _, ok := modifyProjectTypes[storeType]; ok {
			schema = schemas.Schema(&projectschema.Version, storeType)
		} else {
			schema = schemas.Schema(&managementschema.Version, storeType)
		}
		data := getFields(schema, schemas)
		id := schema.ID
		pwdStore.Stores[id] = schema.Store
		pwdStore.Fields[id] = data
		schema.Store = pwdStore

	}
}

func (p *PasswordStore) Create(apiContext *types.APIContext, schema *types.Schema, data map[string]interface{}) (map[string]interface{}, error) {
	if err := p.replacePasswords(data, schema.ID); err != nil {
	}
	return p.Stores[schema.ID].Create(apiContext, schema, data)
}

func (p *PasswordStore) ByID(apiContext *types.APIContext, schema *types.Schema, id string) (map[string]interface{}, error) {
	data, err := p.Stores[schema.ID].ByID(apiContext, schema, id)
	if err != nil {
		return nil, err
	}
	if err := p.assignBack(data, schema.ID); err != nil {
		return nil, err
	}
	return data, err
}

func (p *PasswordStore) Context() types.StorageContext {
	return config.ManagementStorageContext
}

func (p *PasswordStore) Delete(apiContext *types.APIContext, schema *types.Schema, id string) (map[string]interface{}, error) {
	return p.Stores[schema.ID].Delete(apiContext, schema, id)
}

func (p *PasswordStore) List(apiContext *types.APIContext, schema *types.Schema, opt *types.QueryOptions) ([]map[string]interface{}, error) {
	return p.Stores[schema.ID].List(apiContext, schema, opt)
}

func (p *PasswordStore) Update(apiContext *types.APIContext, schema *types.Schema, data map[string]interface{}, id string) (map[string]interface{}, error) {
	if err := p.replacePasswords(data, schema.ID); err != nil {
		return nil, err
	}
	data, err := p.Stores[schema.ID].Update(apiContext, schema, data, id)
	if err != nil {
		return nil, err
	}
	return data, err
}

func (p *PasswordStore) Watch(apiContext *types.APIContext, schema *types.Schema, opt *types.QueryOptions) (chan map[string]interface{}, error) {
	return p.Stores[schema.ID].Watch(apiContext, schema, opt)
}

type fieldInfo struct {
	paths []string
	value string
}

func (p *PasswordStore) replacePasswords(data map[string]interface{}, id string) error {
	var fieldData []fieldInfo
	var path []string
	buildFieldData(convert.ToMapInterface(p.Fields[id]), data, &fieldData, path)

	return p.handlePasswordFields(fieldData, data, id)
}

func (p *PasswordStore) assignBack(data map[string]interface{}, id string) error {
	var fieldData []fieldInfo
	var path []string

	buildFieldData(convert.ToMapInterface(p.Fields[id]), data, &fieldData, path)

	for _, info := range fieldData {
		split := strings.SplitN(info.value, ":", 2)
		if len(split) != 2 {
			continue
		}
		value, err := p.getSecret([]string{"mgmt-secrets", "githubconfig-clientsecret"})
		if err != nil {
			return fmt.Errorf("error getting secret for field %s", info.value)
		}
		values.PutValue(data, value, info.paths...)
	}
	return nil
}

func (p *PasswordStore) handlePasswordFields(fieldData []fieldInfo, data map[string]interface{}, id string) error {
	for _, info := range fieldData {
		var name, namespace string
		if _, ok := data["name"]; ok {
			name = convert.ToString(data["name"])
		} else {
			name = fmt.Sprintf("%s-%s", id, info.paths[len(info.paths)-1])
		}
		if val, ok := data["id"]; ok {
			splitID := strings.Split(convert.ToString(val), ":")
			if len(splitID) == 2 {
				namespace = splitID[1]
			} else if len(splitID) == 1 {
				namespace = splitID[0]
			}
		}
		if namespace == "" {
			namespace = "mgmt-secrets"
		}
		if err := p.createOrUpdateSecrets(info.value, name, namespace); err != nil {
			return err
		}
		values.PutValue(data, fmt.Sprintf("%s:%s", namespace, strings.ToLower(name)), info.paths...)
	}
	return nil
}

func buildFieldData(data1 map[string]interface{}, data2 map[string]interface{}, fieldData *[]fieldInfo, path []string) {
	for key1, val1 := range data1 {
		if val2, ok := data2[key1]; ok {
			if convert.ToString(val1) == separator {
				val := convert.ToString(val2)
				if val != "" {
					split := strings.Split(val, ":")
					if len(split) == 2 && (split[0] == "mgmt-secrets") {
						// rancher referenced
						logrus.Debugf("rancher referenced, continue %s", val)
						continue
					}
				}
				path = append(path, key1)
				*fieldData = append(*fieldData, fieldInfo{path, val})
			} else {
				valArr := convert.ToMapSlice(val2)
				if valArr == nil {
					buildFieldData(convert.ToMapInterface(val1), convert.ToMapInterface(val2), fieldData, append(path, key1))
				} else {
					for _, each := range valArr {
						buildFieldData(convert.ToMapInterface(val1), each, fieldData, append(path, key1))
					}
				}
			}
		}
	}
}

func (p *PasswordStore) createOrUpdateSecrets(data, name, namespace string) error {
	_, err := p.nsStore.Get(namespace, metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		if _, err := p.nsStore.Create(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}); err != nil && !errors.IsAlreadyExists(err) {
			return err
		}
	}
	if err != nil {
		return err
	}
	name = strings.ToLower(name)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		StringData: map[string]string{name: data},
		Type:       corev1.SecretTypeOpaque,
	}
	existing, err := p.secretStore.GetNamespaced(namespace, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = p.secretStore.Create(secret)
		return err
	} else if err != nil {
		return err
	}
	if !reflect.DeepEqual(existing.StringData, secret.StringData) {
		existing.StringData = secret.StringData
		_, err = p.secretStore.Update(existing)
	}
	return err
}

func (p *PasswordStore) getSecret(input []string) (string, error) {
	returned := ""
	secret, err := p.secretStore.GetNamespaced(input[0], input[1], metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	for key, val := range secret.Data {
		if key == input[1] {
			returned = string(val)
		}
	}
	return returned, nil
}

func (p *PasswordStore) deleteSecret(name string, namespace string) error {
	err := p.secretStore.DeleteNamespaced(namespace, name, &metav1.DeleteOptions{})
	if err != nil && errors.IsNotFound(err) {
		return nil
	}
	return err
}

func getFields(schema *types.Schema, schemas *types.Schemas) map[string]interface{} {
	data := map[string]interface{}{}
	for name, field := range schema.ResourceFields {
		fieldType := field.Type
		if strings.HasPrefix(fieldType, "array") {
			fieldType = strings.Split(fieldType, "[")[1]
			fieldType = fieldType[:len(fieldType)-1]
		}
		checkSchema := schemas.Schema(&managementschema.Version, fieldType)
		if checkSchema != nil {
			value := getFields(checkSchema, schemas)
			if len(value) > 0 {
				data[name] = value
			}
		} else {
			if field.Type == "password" {
				data[name] = separator
			}
		}
	}
	return data
}
