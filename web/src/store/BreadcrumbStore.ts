import {observable} from 'mobx'
import {BreadcrumbStatus} from '../model/Breadcrumb'

export class BreadcrumbStore {
    @observable public breadcrumbList: Array<BreadcrumbStatus> = []

    public setBreadcrumbs(breadcrumbs: Array<BreadcrumbStatus>) {
        this.breadcrumbList = breadcrumbs
    }

}